package models

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed registry.json
var registryFS embed.FS

// Canonical file names inside a per-model cache directory (layout v2).
const (
	// PrimaryONNX is the ONNX weights file: models/<id>/model.onnx.
	PrimaryONNX = "model.onnx"
	// PrimaryXML is the OpenVINO IR file: models/<id>/model.xml.
	PrimaryXML = "model.xml"
	// DataSuffix is appended to the primary file for ONNX external weights,
	// e.g. models/<id>/model.onnx.data.
	DataSuffix = ".data"
)

// Entry is one model in the bundled registry.
type Entry struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Scale        int      `json:"scale"`
	Kind         string   `json:"kind"` // photo|anime|compressed|general
	Description  string   `json:"description"`
	SizeMB       float64  `json:"sizeMB"`
	URL          string   `json:"url"`
	FallbackURLs []string `json:"fallbackUrls,omitempty"`
	SHA256       string   `json:"sha256,omitempty"`

	// Registry v2 metadata (mirrored into the per-model sidecar on install).
	Arch       string   `json:"arch,omitempty"`       // rrdb|swin2sr|cugan|srvgg
	License    string   `json:"license,omitempty"`    // SPDX-ish licence of the weights
	TileAlign  int      `json:"tileAlign,omitempty"`  // tile size alignment, 0 = none
	Norm       string   `json:"norm,omitempty"`       // "0-1"|"0-255"|"-1-1"
	ExtraFiles []string `json:"extraFiles,omitempty"` // sibling files, e.g. "model.onnx.data"
}

// Registry is the embedded model catalogue.
type Registry struct {
	Version int     `json:"version"`
	Updated string  `json:"updated"`
	Models  []Entry `json:"models"`
}

// Load reads the embedded registry.
func Load() (*Registry, error) {
	data, err := registryFS.ReadFile("registry.json")
	if err != nil {
		return nil, err
	}
	return ParseRegistry(data)
}

// ParseRegistry decodes and sanity checks a registry document.
func ParseRegistry(data []byte) (*Registry, error) {
	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("chronos: parse model registry: %w", err)
	}
	if len(reg.Models) == 0 {
		return nil, fmt.Errorf("chronos: empty model registry")
	}
	return &reg, nil
}

// Lookup finds a model by id.
func (r *Registry) Lookup(id string) (Entry, bool) {
	for _, m := range r.Models {
		if m.ID == id {
			return m, true
		}
	}
	return Entry{}, false
}

// ValidSHA256 reports whether s is a 64 character hexadecimal SHA-256 digest.
func ValidSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// Info is the frontend-facing model view (docs/CONTRACTS.md §3.2).
type Info struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Scale       int     `json:"scale"`
	Kind        string  `json:"kind"`
	Description string  `json:"description"`
	SizeMB      float64 `json:"sizeMb"`
	Installed   bool    `json:"installed"`
	Path        string  `json:"path"`
	URL         string  `json:"url"`

	// Registry v2 / layout v2 additions (contract §3.2 v3 fields are additive).
	License string `json:"license,omitempty"`
	Arch    string `json:"arch,omitempty"`
	Sidecar bool   `json:"sidecar"` // model.json exists next to the weights
}

// Migration records one legacy flat model file (models/<id>.onnx|.xml) that was
// moved into the v2 layout, or the error that prevented it. Migrations never
// fail the calling API: they are observable through Store.Migrations().
type Migration struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
	Err  error  `json:"-"`
}

// ErrText returns the migration error as a string ("" on success).
func (m Migration) ErrText() string {
	if m.Err == nil {
		return ""
	}
	return m.Err.Error()
}

// Store manages the on-disk model cache.
//
// Layout v2: models/<id>/model.onnx (+ optional model.onnx.data) and
// models/<id>/model.json (sidecar). Legacy flat files models/<id>.onnx|.xml are
// migrated on first access, atomically and idempotently; migration failures
// (e.g. a read-only cache dir) degrade to "not installed" instead of erroring.
type Store struct {
	dir      string
	registry *Registry
	client   *http.Client

	mu       sync.Mutex
	logMu    sync.RWMutex
	logf     func(format string, args ...any)
	migrated []Migration
}

// DefaultDir returns the per-user model cache directory. It falls back to the
// config dir when the cache dir cannot be created (e.g. restricted ACLs).
func DefaultDir() (string, error) {
	candidates := make([]string, 0, 2)
	if base, err := os.UserCacheDir(); err == nil {
		candidates = append(candidates, filepath.Join(base, "Chronos", "models"))
	}
	if base, err := os.UserConfigDir(); err == nil {
		candidates = append(candidates, filepath.Join(base, "Chronos", "models"))
	}
	var lastErr error
	for _, dir := range candidates {
		if err := os.MkdirAll(dir, 0o755); err == nil {
			return dir, nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = os.ErrNotExist
	}
	return "", lastErr
}

// NewStore loads the embedded registry and makes sure dir exists.
func NewStore(dir string, reg *Registry) (*Store, error) {
	if reg == nil {
		var err error
		reg, err = Load()
		if err != nil {
			return nil, err
		}
	}
	if dir == "" {
		d, err := DefaultDir()
		if err != nil {
			return nil, err
		}
		dir = d
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("chronos: create model dir: %w", err)
	}
	return &Store{
		dir:      dir,
		registry: reg,
		client:   &http.Client{Timeout: 30 * time.Minute},
	}, nil
}

// Dir is the model cache directory.
func (s *Store) Dir() string { return s.dir }

// Registry exposes the catalogue.
func (s *Store) Registry() *Registry { return s.registry }

// ModelDir is the per-model directory: <cache>/<id>.
func (s *Store) ModelDir(e Entry) string { return filepath.Join(s.dir, e.ID) }

// PrimaryName is the weights file name for an entry: "model.xml" for OpenVINO
// IR downloads, "model.onnx" otherwise.
func PrimaryName(e Entry) string {
	if strings.HasSuffix(strings.ToLower(e.URL), ".xml") {
		return PrimaryXML
	}
	return PrimaryONNX
}

// ModelFile is the primary weights path: <cache>/<id>/model.onnx|model.xml.
func (s *Store) ModelFile(e Entry) string {
	return filepath.Join(s.ModelDir(e), PrimaryName(e))
}

// SidecarPath is the sidecar path: <cache>/<id>/model.json.
func (s *Store) SidecarPath(e Entry) string {
	return filepath.Join(s.ModelDir(e), SidecarFile)
}

// PathFor derives the cache path for a registry entry.
//
// Deprecated: use ModelFile (weights) or ModelDir (directory). PathFor is kept
// as an alias so existing callers keep compiling; it returns the v2 layout path.
func (s *Store) PathFor(e Entry) string { return s.ModelFile(e) }

// SetLogger installs an optional migration logger. It is safe to call while the
// store is in use; the logger itself must be safe for concurrent calls and must
// not call back into this Store.
func (s *Store) SetLogger(logf func(format string, args ...any)) {
	s.logMu.Lock()
	s.logf = logf
	s.logMu.Unlock()
}

// Migrations returns the legacy->v2 migrations observed by this store so far.
func (s *Store) Migrations() []Migration {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Migration, len(s.migrated))
	copy(out, s.migrated)
	return out
}

func (s *Store) log(format string, args ...any) {
	s.logMu.RLock()
	logf := s.logf
	s.logMu.RUnlock()
	if logf != nil {
		logf(format, args...)
	}
}

// List reports every model with installed state, migrating legacy layouts on
// first access.
func (s *Store) List() []Info {
	out := make([]Info, 0, len(s.registry.Models))
	for _, e := range s.registry.Models {
		path, installed := s.ensureLayout(e)
		_, sidecarErr := os.Stat(s.SidecarPath(e))
		out = append(out, Info{
			ID:          e.ID,
			Name:        e.Name,
			Scale:       e.Scale,
			Kind:        e.Kind,
			Description: e.Description,
			SizeMB:      e.SizeMB,
			Installed:   installed,
			Path:        path,
			URL:         e.URL,
			License:     e.License,
			Arch:        e.Arch,
			Sidecar:     sidecarErr == nil,
		})
	}
	return out
}

// Resolve returns the local weights path for an installed model and whether it
// is installed. Migration failures degrade to installed=false with a nil error.
func (s *Store) Resolve(id string) (string, bool, error) {
	e, ok := s.registry.Lookup(id)
	if !ok {
		return "", false, fmt.Errorf("chronos: unknown model %q", id)
	}
	path, installed := s.ensureLayout(e)
	return path, installed, nil
}

// MigrateAll proactively migrates every legacy flat model file. It is the
// backing call of `chronos models migrate`; List/Resolve do the same lazily.
func (s *Store) MigrateAll() []Migration {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Migration
	for _, e := range s.registry.Models {
		if _, ok := s.installedPath(e); ok {
			continue
		}
		if m, ok := s.migrateEntryLocked(e); ok {
			s.migrated = append(s.migrated, m)
			out = append(out, m)
		}
	}
	return out
}

// installedPath returns the first existing primary weights file of an entry.
func (s *Store) installedPath(e Entry) (string, bool) {
	for _, name := range []string{PrimaryName(e), PrimaryONNX, PrimaryXML} {
		p := filepath.Join(s.ModelDir(e), name)
		if isInstalledFile(p) {
			return p, true
		}
	}
	return "", false
}

// ensureLayout returns the installed weights path after migrating the legacy
// flat layout when needed. A failed migration never returns an error: the model
// is reported as not installed.
func (s *Store) ensureLayout(e Entry) (string, bool) {
	if p, ok := s.installedPath(e); ok {
		return p, true
	}
	var migration Migration
	migrated := false
	s.mu.Lock()
	if p, ok := s.installedPath(e); ok {
		s.mu.Unlock()
		return p, true
	}
	if m, ok := s.migrateEntryLocked(e); ok {
		s.migrated = append(s.migrated, m)
		migration, migrated = m, true
	}
	s.mu.Unlock()
	if migrated {
		if migration.Err == nil {
			s.log("chronos: migrated legacy model %s: %s -> %s", migration.ID, migration.From, migration.To)
		} else {
			s.log("chronos: migrate legacy model %s: %v", migration.ID, migration.Err)
		}
	}
	if p, ok := s.installedPath(e); ok {
		return p, true
	}
	return s.ModelFile(e), false
}

// migrateEntryLocked copies a legacy flat model (and its companion files) into
// the v2 layout using temp-file + rename. Callers must hold s.mu.
func (s *Store) migrateEntryLocked(e Entry) (Migration, bool) {
	legacy := s.findLegacy(e)
	if legacy == "" {
		return Migration{}, false
	}
	// The legacy extension is authoritative for the content type.
	name := PrimaryONNX
	if strings.EqualFold(filepath.Ext(legacy), ".xml") {
		name = PrimaryXML
	}
	dir := s.ModelDir(e)
	target := filepath.Join(dir, name)
	m := Migration{ID: e.ID, From: legacy, To: target}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.Err = err
		return m, true
	}
	// Companion files first (e.g. <id>.onnx.data -> model.onnx.data) so the
	// primary weights file remains the "installed" gate.
	for _, companion := range s.legacyCompanions(legacy) {
		dest := target + strings.TrimPrefix(filepath.Base(companion), filepath.Base(legacy))
		if err := copyFileAtomic(companion, dest, 0o644); err != nil {
			m.Err = fmt.Errorf("companion %s: %w", filepath.Base(companion), err)
			return m, true
		}
	}
	if err := copyFileAtomic(legacy, target, 0o644); err != nil {
		m.Err = err
		return m, true
	}
	// Best effort: drop the legacy flat files. A failure here is harmless, the
	// v2 file wins on the next access.
	for _, old := range append(s.legacyCompanions(legacy), legacy) {
		if err := os.Remove(old); err != nil && !os.IsNotExist(err) {
			s.log("chronos: remove legacy model file %s: %v", old, err)
		}
	}
	s.ensureSidecar(e)
	return m, true
}

// findLegacy returns the legacy flat weights file for an entry, preferring the
// extension implied by the registry URL.
func (s *Store) findLegacy(e Entry) string {
	names := []string{e.ID + ".onnx", e.ID + ".xml"}
	if PrimaryName(e) == PrimaryXML {
		names[0], names[1] = names[1], names[0]
	}
	for _, name := range names {
		p := filepath.Join(s.dir, name)
		if isInstalledFile(p) {
			return p
		}
	}
	return ""
}

// legacyCompanions lists legacy flat companion files of a legacy weights file,
// e.g. models/<id>.onnx.data for models/<id>.onnx.
func (s *Store) legacyCompanions(legacy string) []string {
	base := filepath.Base(legacy) + "."
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, de := range entries {
		if de.IsDir() || !strings.HasPrefix(de.Name(), base) {
			continue
		}
		out = append(out, filepath.Join(s.dir, de.Name()))
	}
	return out
}

// ensureSidecar writes models/<id>/model.json derived from the registry when the
// model has no sidecar yet. Errors are logged, never returned.
func (s *Store) ensureSidecar(e Entry) {
	p := s.SidecarPath(e)
	if _, err := os.Stat(p); err == nil {
		return
	}
	sc := SidecarForEntry(e)
	if err := sc.Write(p); err != nil {
		s.log("chronos: write sidecar %s: %v", p, err)
	}
}

// Pull downloads a model (trying fallback URLs) into the v2 layout and verifies
// the hash when the registry provides one. progress receives downloaded/total
// bytes (-1 total if the server does not send Content-Length).
func (s *Store) Pull(ctx context.Context, id string, progress func(done, total int64)) (string, error) {
	e, ok := s.registry.Lookup(id)
	if !ok {
		return "", fmt.Errorf("chronos: unknown model %q", id)
	}
	if dest, installed := s.ensureLayout(e); installed {
		return dest, nil
	}
	dir := s.ModelDir(e)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("chronos: create model dir: %w", err)
	}
	urls := append([]string{e.URL}, e.FallbackURLs...)
	var lastErr error
	for _, u := range urls {
		if u == "" {
			continue
		}
		dest, err := s.pullFrom(ctx, e, u, progress)
		if err != nil {
			lastErr = err
			continue
		}
		return dest, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no download url")
	}
	return "", fmt.Errorf("chronos: download model %s: %w", id, lastErr)
}

// pullFrom downloads one URL (plus its declared companion files) and commits the
// files into the model directory. The weights file is renamed last so a
// partially downloaded model is never reported as installed.
func (s *Store) pullFrom(ctx context.Context, e Entry, rawURL string, progress func(done, total int64)) (string, error) {
	dir := s.ModelDir(e)
	primary := PrimaryName(e)
	tmpPrimary, err := s.fetch(ctx, rawURL, dir, e.SHA256, progress)
	if err != nil {
		return "", err
	}
	var committed []string
	cleanup := func() {
		os.Remove(tmpPrimary)
		for _, p := range committed {
			os.Remove(p)
		}
	}
	for _, extra := range e.ExtraFiles {
		extra = filepath.ToSlash(strings.TrimSpace(extra))
		if extra == "" {
			continue
		}
		extraURL := extraFileURL(rawURL, primary, extra)
		if extraURL == "" {
			cleanup()
			return "", fmt.Errorf("chronos: cannot derive companion url for %q", extra)
		}
		local := filepath.Join(dir, filepath.FromSlash(extra))
		if parent := filepath.Dir(local); parent != "" && parent != "." {
			if err := os.MkdirAll(parent, 0o755); err != nil {
				cleanup()
				return "", err
			}
		}
		tmpExtra, err := s.fetch(ctx, extraURL, filepath.Dir(local), "", nil)
		if err != nil {
			cleanup()
			return "", fmt.Errorf("chronos: companion %s: %w", extra, err)
		}
		if err := renameInto(tmpExtra, local); err != nil {
			os.Remove(tmpExtra)
			cleanup()
			return "", err
		}
		committed = append(committed, local)
	}
	dest := filepath.Join(dir, primary)
	if err := renameInto(tmpPrimary, dest); err != nil {
		os.Remove(tmpPrimary)
		cleanup()
		return "", err
	}
	s.ensureSidecar(e)
	return dest, nil
}

// extraFileURL derives the remote URL of a companion file from the weights URL:
// "model.onnx.data" becomes <weights-url>.data, anything else is resolved next
// to the weights file.
func extraFileURL(baseURL, primaryName, extra string) string {
	if strings.HasPrefix(extra, primaryName) {
		return baseURL + strings.TrimPrefix(extra, primaryName)
	}
	if i := strings.LastIndex(baseURL, "/"); i >= 0 {
		return baseURL[:i+1] + path.Base(extra)
	}
	return ""
}

// fetch streams url into a temp file in dir, optionally verifying sha256, and
// returns the temp path. The caller owns the temp file.
func (s *Store) fetch(ctx context.Context, url, dir, wantSHA string, progress func(done, total int64)) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp(dir, ".chronos-dl-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	hasher := sha256.New()
	writer := io.MultiWriter(tmp, hasher)
	total := resp.ContentLength
	var done int64
	buf := make([]byte, 1<<20)
	last := time.Now()
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := writer.Write(buf[:n]); err != nil {
				cleanup()
				return "", err
			}
			done += int64(n)
			if progress != nil && time.Since(last) > 200*time.Millisecond {
				last = time.Now()
				progress(done, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			cleanup()
			return "", readErr
		}
	}
	if progress != nil {
		progress(done, total)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	if wantSHA != "" {
		got := hex.EncodeToString(hasher.Sum(nil))
		if !strings.EqualFold(got, wantSHA) {
			os.Remove(tmpName)
			return "", fmt.Errorf("sha256 mismatch: got %s", got)
		}
	}
	return tmpName, nil
}

// Import copies a user-supplied model file into the cache under the given id,
// together with an optional "<src>.data" companion and a derived sidecar.
func (s *Store) Import(id, src string) (string, error) {
	e, ok := s.registry.Lookup(id)
	if !ok {
		return "", fmt.Errorf("chronos: unknown model %q", id)
	}
	dir := s.ModelDir(e)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, PrimaryName(e))
	if err := copyFileAtomic(src, dest, 0o644); err != nil {
		return "", err
	}
	if isInstalledFile(src + DataSuffix) {
		if err := copyFileAtomic(src+DataSuffix, dest+DataSuffix, 0o644); err != nil {
			return "", err
		}
	}
	s.ensureSidecar(e)
	return dest, nil
}

// isInstalledFile reports whether path is a non-empty regular file.
func isInstalledFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir() && st.Size() > 0
}

// renameInto renames tmp to dest. When dest already exists and is valid (a
// concurrent writer won the race, or a retry) the temp file is dropped and nil
// is returned.
func renameInto(tmp, dest string) error {
	if err := os.Rename(tmp, dest); err != nil {
		if isInstalledFile(dest) {
			os.Remove(tmp)
			return nil
		}
		return err
	}
	return nil
}

// copyFileAtomic copies src to dest via a temp file in the destination
// directory and an atomic rename.
func copyFileAtomic(src, dest string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	dir := filepath.Dir(dest)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(dir, ".chronos-copy-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	if _, err := io.Copy(tmp, in); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if perm != 0 {
		_ = os.Chmod(tmpName, perm)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// WriteFileAtomic writes data to path atomically (temp file + rename), creating
// parent directories as needed.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(dir, ".chronos-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if perm != 0 {
		_ = os.Chmod(tmpName, perm)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
