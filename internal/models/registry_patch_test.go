package models

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testRegistryJSON = `{
  "version": 2,
  "x-note": "keep me",
  "updated": "2026-11-08",
  "models": [
    {
      "id": "a",
      "name": "Model A",
      "sizeMB": 4.9,
      "custom": {
        "z": 1
      },
      "sha256": ""
    },
    {
      "id": "b",
      "name": "Model B",
      "fallbackUrls": [
        "https://example.invalid/mirror.onnx"
      ],
      "sha256": ""
    }
  ]
}
`

func TestPatchRegistryPreservesOrderAndUnknownFields(t *testing.T) {
	sumA, sumB := sha256Hex("a"), sha256Hex("b")
	out, err := PatchRegistry([]byte(testRegistryJSON), map[string]EntryPatch{
		"a": {SHA256: sumA},
		"b": {SHA256: sumB, ExtraFiles: []string{PrimaryONNX + DataSuffix}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(out), "\n") {
		t.Fatal("patched registry does not end with a newline")
	}
	if !strings.Contains(string(out), "\n  \"version\"") {
		t.Fatalf("patched registry is not 2-space indented:\n%s", out)
	}
	members, err := decodeOrderedObject(out)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, m := range members {
		keys = append(keys, m.Key)
	}
	if got, want := strings.Join(keys, ","), "version,x-note,updated,models"; got != want {
		t.Fatalf("top-level key order = %q, want %q", got, want)
	}
	if !strings.Contains(string(out), `"x-note": "keep me"`) {
		t.Fatalf("unknown top-level field dropped:\n%s", out)
	}
	var models []map[string]json.RawMessage
	if err := json.Unmarshal(members[3].Val, &models); err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %d", len(models))
	}
	var sum string
	if err := json.Unmarshal(models[0]["sha256"], &sum); err != nil {
		t.Fatal(err)
	}
	if sum != sumA {
		t.Fatalf("model a sha256 = %q", sum)
	}
	if _, ok := models[0]["custom"]; !ok {
		t.Fatal("unknown per-model field dropped")
	}
	if !strings.Contains(string(models[0]["custom"]), `"z": 1`) {
		t.Fatalf("unknown per-model field changed: %s", models[0]["custom"])
	}
	if err := json.Unmarshal(models[1]["sha256"], &sum); err != nil {
		t.Fatal(err)
	}
	if sum != sumB {
		t.Fatalf("model b sha256 = %q", sum)
	}
	var extras []string
	if err := json.Unmarshal(models[1]["extraFiles"], &extras); err != nil {
		t.Fatalf("extraFiles: %v", err)
	}
	if len(extras) != 1 || extras[0] != PrimaryONNX+DataSuffix {
		t.Fatalf("extraFiles = %v", extras)
	}
}

func TestPatchRegistryNoChanges(t *testing.T) {
	in := []byte(testRegistryJSON)
	out, err := PatchRegistry(in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, in) {
		t.Fatal("empty patch set must not rewrite the registry")
	}
	out, err = PatchRegistry(in, map[string]EntryPatch{"unknown": {SHA256: sha256Hex("x")}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, in) {
		t.Fatal("unknown id patch must not rewrite the registry")
	}
}

func TestPatchRegistryErrors(t *testing.T) {
	if _, err := PatchRegistry([]byte("[]"), map[string]EntryPatch{"a": {SHA256: sha256Hex("a")}}); err == nil {
		t.Fatal("expected error for a non-object registry")
	}
	if _, err := PatchRegistry([]byte(`{"version":2}`), map[string]EntryPatch{"a": {SHA256: sha256Hex("a")}}); err == nil {
		t.Fatal("expected error for a registry without models")
	}
	if _, err := PatchRegistry([]byte(`{"version":2,"models":[{"id":"a","sha256":""}]}`), map[string]EntryPatch{"a": {SHA256: "not-a-hash"}}); err != nil {
		t.Fatalf("patching a plain string value should not fail: %v", err)
	}
}

func TestPatchRegistryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")
	if err := os.WriteFile(path, []byte(testRegistryJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256Hex("a")
	n, err := PatchRegistryFile(path, map[string]EntryPatch{
		"a": {SHA256: sum},
		"b": {SHA256: sha256Hex("b")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("patched models = %d, want 2", n)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := ParseRegistry(data)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := reg.Lookup("a")
	if !ok || e.SHA256 != sum {
		t.Fatalf("patched registry lookup = %+v %v", e, ok)
	}
	// Unknown fields survive a write to disk as well.
	if !strings.Contains(string(data), `"x-note": "keep me"`) {
		t.Fatalf("unknown field dropped on disk write:\n%s", data)
	}

	// A missing file is an error, not a silent success.
	if _, err := PatchRegistryFile(filepath.Join(dir, "missing.json"), nil); err == nil {
		t.Fatal("expected error for a missing registry file")
	}
}

func TestPatchRegistryAddsMissingKeys(t *testing.T) {
	in := []byte(`{"version": 2, "models": [{"id": "a", "name": "A"}]}`)
	sum := sha256Hex("a")
	out, err := PatchRegistry(in, map[string]EntryPatch{
		"a": {SHA256: sum, ExtraFiles: []string{PrimaryONNX + DataSuffix}},
	})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := ParseRegistry(out)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := reg.Lookup("a")
	if !ok || e.SHA256 != sum {
		t.Fatalf("lookup = %+v %v", e, ok)
	}
	if len(e.ExtraFiles) != 1 || e.ExtraFiles[0] != PrimaryONNX+DataSuffix {
		t.Fatalf("extraFiles = %v", e.ExtraFiles)
	}
	if !strings.Contains(string(out), `"name": "A"`) {
		t.Fatalf("existing field dropped:\n%s", out)
	}
}
