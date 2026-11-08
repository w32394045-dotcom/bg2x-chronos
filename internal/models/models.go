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
	"path/filepath"
	"strings"
	"time"
)

//go:embed registry.json
var registryFS embed.FS

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
}

// Store manages the on-disk model cache.
type Store struct {
	dir      string
	registry *Registry
	client   *http.Client
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

// PathFor derives the cache path for a registry entry.
func (s *Store) PathFor(e Entry) string {
	name := e.ID + ".onnx"
	if strings.HasSuffix(strings.ToLower(e.URL), ".xml") {
		name = e.ID + ".xml"
	}
	return filepath.Join(s.dir, name)
}

// List reports every model with installed state.
func (s *Store) List() []Info {
	out := make([]Info, 0, len(s.registry.Models))
	for _, e := range s.registry.Models {
		path := s.PathFor(e)
		installed := false
		if st, err := os.Stat(path); err == nil && st.Size() > 0 {
			installed = true
		}
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
		})
	}
	return out
}

// Resolve returns the local path for an installed model.
func (s *Store) Resolve(id string) (string, bool, error) {
	e, ok := s.registry.Lookup(id)
	if !ok {
		return "", false, fmt.Errorf("chronos: unknown model %q", id)
	}
	path := s.PathFor(e)
	if st, err := os.Stat(path); err == nil && st.Size() > 0 {
		return path, true, nil
	}
	return path, false, nil
}

// Pull downloads a model (trying fallback URLs) and verifies the hash when the
// registry provides one. progress receives downloaded/total bytes (-1 total if
// the server does not send Content-Length).
func (s *Store) Pull(ctx context.Context, id string, progress func(done, total int64)) (string, error) {
	e, ok := s.registry.Lookup(id)
	if !ok {
		return "", fmt.Errorf("chronos: unknown model %q", id)
	}
	dest := s.PathFor(e)
	if st, err := os.Stat(dest); err == nil && st.Size() > 0 {
		return dest, nil
	}
	urls := append([]string{e.URL}, e.FallbackURLs...)
	var lastErr error
	for _, u := range urls {
		if u == "" {
			continue
		}
		if err := s.download(ctx, u, dest, e.SHA256, progress); err != nil {
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

func (s *Store) download(ctx context.Context, url, dest, wantSHA string, progress func(done, total int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".chronos-dl-*")
	if err != nil {
		return err
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
				return err
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
			return readErr
		}
	}
	if progress != nil {
		progress(done, total)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if wantSHA != "" {
		got := hex.EncodeToString(hasher.Sum(nil))
		if !strings.EqualFold(got, wantSHA) {
			os.Remove(tmpName)
			return fmt.Errorf("sha256 mismatch: got %s", got)
		}
	}
	return os.Rename(tmpName, dest)
}

// Import copies a user-supplied model file into the cache under the given id.
func (s *Store) Import(id, src string) (string, error) {
	e, ok := s.registry.Lookup(id)
	if !ok {
		return "", fmt.Errorf("chronos: unknown model %q", id)
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	dest := s.PathFor(e)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	tmp := dest + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return "", err
	}
	return dest, nil
}
