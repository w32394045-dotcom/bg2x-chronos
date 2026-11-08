package models

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// EntryPatch contains the registry fields the hash command can update.
type EntryPatch struct {
	SHA256 string
	// ExtraFiles, when non-nil, replaces the per-model extraFiles list.
	ExtraFiles []string
}

// PatchRegistryFile applies patches to a registry JSON file in place. It
// preserves the original field order and any unknown fields, normalising the
// output to 2-space indented JSON. It returns the number of patched models.
func PatchRegistryFile(path string, patches map[string]EntryPatch) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	out, changed, err := patchRegistry(data, patches)
	if err != nil {
		return 0, err
	}
	if changed == 0 {
		return 0, nil
	}
	if err := WriteFileAtomic(path, out, 0o644); err != nil {
		return 0, err
	}
	return changed, nil
}

// PatchRegistry applies patches to a registry document, preserving field order
// and unknown fields. It returns the same bytes when nothing changes.
func PatchRegistry(data []byte, patches map[string]EntryPatch) ([]byte, error) {
	out, _, err := patchRegistry(data, patches)
	return out, err
}

func patchRegistry(data []byte, patches map[string]EntryPatch) ([]byte, int, error) {
	if len(patches) == 0 {
		return data, 0, nil
	}
	top, err := decodeOrderedObject(data)
	if err != nil {
		return nil, 0, fmt.Errorf("chronos: registry: %w", err)
	}
	modelsIdx := -1
	for i := range top {
		if top[i].Key == "models" {
			modelsIdx = i
			break
		}
	}
	if modelsIdx < 0 {
		return nil, 0, fmt.Errorf("chronos: registry: no %q array", "models")
	}
	var models []json.RawMessage
	if err := json.Unmarshal(top[modelsIdx].Val, &models); err != nil {
		return nil, 0, fmt.Errorf("chronos: registry: %q: %w", "models", err)
	}
	updated := make([]json.RawMessage, len(models))
	copy(updated, models)
	changed := 0
	for i, raw := range models {
		var head struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			return nil, 0, fmt.Errorf("chronos: registry: model %d: %w", i, err)
		}
		patch, ok := patches[head.ID]
		if !ok {
			continue
		}
		updates := make(map[string]json.RawMessage, 2)
		if patch.SHA256 != "" {
			value, _ := json.Marshal(patch.SHA256)
			updates["sha256"] = value
		}
		if patch.ExtraFiles != nil {
			value, _ := json.Marshal(patch.ExtraFiles)
			updates["extraFiles"] = value
		}
		if len(updates) == 0 {
			continue
		}
		patched, err := patchObject(raw, updates)
		if err != nil {
			return nil, 0, fmt.Errorf("chronos: registry: model %q: %w", head.ID, err)
		}
		updated[i] = patched
		changed++
	}
	if changed == 0 {
		return data, 0, nil
	}
	var arr bytes.Buffer
	arr.WriteByte('[')
	for i, raw := range updated {
		if i > 0 {
			arr.WriteByte(',')
		}
		arr.Write(raw)
	}
	arr.WriteByte(']')
	top[modelsIdx].Val = arr.Bytes()

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, encodeOrderedObject(top), "", "  "); err != nil {
		return nil, 0, fmt.Errorf("chronos: registry: indent: %w", err)
	}
	pretty.WriteByte('\n')
	return pretty.Bytes(), changed, nil
}

// jsonMember is one key/value pair of a JSON object, kept in source order.
type jsonMember struct {
	Key string
	Val json.RawMessage
}

// decodeOrderedObject decodes a JSON object into its ordered members.
func decodeOrderedObject(raw []byte) ([]jsonMember, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("want a json object")
	}
	var members []jsonMember
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("want a json object key")
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		members = append(members, jsonMember{Key: key, Val: value})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return members, nil
}

// encodeOrderedObject encodes ordered members as a compact JSON object.
func encodeOrderedObject(members []jsonMember) []byte {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, m := range members {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyJSON, _ := json.Marshal(m.Key)
		buf.Write(keyJSON)
		buf.WriteByte(':')
		buf.Write(m.Val)
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

// patchObject replaces the given keys of a JSON object while keeping the
// original key order; missing keys are appended in sorted order.
func patchObject(raw []byte, updates map[string]json.RawMessage) ([]byte, error) {
	members, err := decodeOrderedObject(raw)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(updates))
	for i := range members {
		if value, ok := updates[members[i].Key]; ok {
			members[i].Val = value
			seen[members[i].Key] = true
		}
	}
	missing := make([]string, 0, len(updates))
	for key := range updates {
		if !seen[key] {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	for _, key := range missing {
		members = append(members, jsonMember{Key: key, Val: updates[key]})
	}
	return encodeOrderedObject(members), nil
}
