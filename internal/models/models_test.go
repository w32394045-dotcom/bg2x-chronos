package models

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const fakeModel = "chronos-fake-onnx-bytes"

func fakeRegistry(url string) *Registry {
	sum := sha256.Sum256([]byte(fakeModel))
	return &Registry{
		Version: 1,
		Models: []Entry{{
			ID:          "fake-x2",
			Name:        "Fake x2",
			Scale:       2,
			Kind:        "general",
			Description: "test model",
			SizeMB:      0.1,
			URL:         url,
			SHA256:      hex.EncodeToString(sum[:]),
		}},
	}
}

func TestEmbeddedRegistryLoads(t *testing.T) {
	reg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Models) < 8 {
		t.Fatalf("expected the upstream model catalogue, got %d entries", len(reg.Models))
	}
	e, ok := reg.Lookup("real-esrgan-x4plus")
	if !ok {
		t.Fatal("real-esrgan-x4plus missing")
	}
	if e.Scale != 4 || !strings.Contains(e.URL, "onnx") {
		t.Fatalf("unexpected entry %+v", e)
	}
	if _, ok := reg.Lookup("nope"); ok {
		t.Fatal("lookup of unknown model succeeded")
	}
}

func TestListReportsInstalledState(t *testing.T) {
	dir := t.TempDir()
	reg := fakeRegistry("https://example.invalid/fake.onnx")
	store, err := NewStore(dir, reg)
	if err != nil {
		t.Fatal(err)
	}
	list := store.List()
	if len(list) != 1 || list[0].Installed {
		t.Fatalf("list = %+v", list)
	}
	if err := os.WriteFile(store.PathFor(reg.Models[0]), []byte(fakeModel), 0o644); err != nil {
		t.Fatal(err)
	}
	list = store.List()
	if !list[0].Installed {
		t.Fatalf("expected installed, list = %+v", list)
	}
	path, installed, err := store.Resolve("fake-x2")
	if err != nil || !installed || path == "" {
		t.Fatalf("resolve = %q %v %v", path, installed, err)
	}
	if _, _, err := store.Resolve("ghost"); err == nil {
		t.Fatal("expected error for unknown model")
	}
}

func TestPullVerifiesAndCaches(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Length", strconv.Itoa(len(fakeModel)))
		_, _ = w.Write([]byte(fakeModel))
	}))
	defer srv.Close()

	store, err := NewStore(t.TempDir(), fakeRegistry(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	var progressCalls int
	path, err := store.Pull(t.Context(), "fake-x2", func(done, total int64) {
		progressCalls++
		if done <= 0 {
			t.Errorf("progress done = %d", done)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != fakeModel {
		t.Fatalf("content = %q", got)
	}
	if progressCalls == 0 {
		t.Fatal("no progress reported")
	}
	// Second pull must hit the cache, not the server.
	if _, err := store.Pull(t.Context(), "fake-x2", nil); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("server hits = %d, want 1", hits)
	}
}

func TestPullHashMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("tampered"))
	}))
	defer srv.Close()
	reg := fakeRegistry(srv.URL)
	store, err := NewStore(t.TempDir(), reg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pull(t.Context(), "fake-x2", nil); err == nil {
		t.Fatal("expected sha256 mismatch")
	}
	if _, statErr := os.Stat(store.PathFor(reg.Models[0])); !os.IsNotExist(statErr) {
		t.Fatal("corrupt download must not be installed")
	}
}

func TestPullFallsBackToMirror(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(fakeModel))
	}))
	defer good.Close()

	reg := fakeRegistry(bad.URL)
	sum := sha256.Sum256([]byte(fakeModel))
	reg.Models[0].FallbackURLs = []string{good.URL}
	reg.Models[0].SHA256 = hex.EncodeToString(sum[:])
	store, err := NewStore(t.TempDir(), reg)
	if err != nil {
		t.Fatal(err)
	}
	path, err := store.Pull(t.Context(), "fake-x2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestImportCopiesLocalFile(t *testing.T) {
	dir := t.TempDir()
	reg := fakeRegistry("https://example.invalid/fake.onnx")
	store, err := NewStore(dir, reg)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "local.onnx")
	if err := os.WriteFile(src, []byte(fakeModel), 0o644); err != nil {
		t.Fatal(err)
	}
	dest, err := store.Import("fake-x2", src)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dest); string(got) != fakeModel {
		t.Fatalf("content = %q", got)
	}
}
