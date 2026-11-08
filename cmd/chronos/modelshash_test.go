package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/w32394045-dotcom/bg2x-chronos/internal/models"
)

const (
	testOID  = "c2abbfe0cc8e685b5e11964970f8ebe3d24072e904fd5545a30ac31ec1e110db"
	testOID2 = "09b757accd747d7e423c1d352b3e8f23e77cc5742d04bae958d4eb8082b76fa4"
)

func hfTreeJSON(paths ...string) string {
	type file struct {
		Type string `json:"type"`
		Path string `json:"path"`
		Size int64  `json:"size"`
		LFS  *struct {
			OID  string `json:"oid"`
			Size int64  `json:"size"`
		} `json:"lfs,omitempty"`
	}
	files := make([]file, 0, len(paths))
	for i, p := range paths {
		oid := testOID
		if i%2 == 1 {
			oid = testOID2
		}
		f := file{Type: "file", Path: p, Size: int64(1024 * (i + 1))}
		f.LFS = &struct {
			OID  string `json:"oid"`
			Size int64  `json:"size"`
		}{OID: oid, Size: f.Size}
		files = append(files, f)
	}
	data, _ := json.Marshal(files)
	return string(data)
}

func newHFServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/api/models/owner/a/tree/"):
			_, _ = w.Write([]byte(hfTreeJSON("model.onnx", "model.onnx.data")))
		case strings.Contains(r.URL.Path, "/api/models/owner/b/tree/"):
			_, _ = w.Write([]byte(hfTreeJSON("other.onnx", "keep.onnx")))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testRegistry() *models.Registry {
	return &models.Registry{
		Version: 2,
		Updated: "2026-11-08",
		Models: []models.Entry{
			{
				ID: "a", Name: "A", Scale: 4, URL: "https://huggingface.co/owner/a/resolve/main/model.onnx",
			},
			{
				ID: "b", Name: "B", Scale: 4, URL: "https://huggingface.co/owner/b/resolve/main/missing.onnx",
			},
		},
	}
}

func newTestResolver(srv *httptest.Server) *models.HFResolver {
	resolver := models.NewHFResolver()
	resolver.APIBase = srv.URL
	resolver.Client = srv.Client()
	return resolver
}

func TestResolveRegistryHashes(t *testing.T) {
	srv := newHFServer(t)
	results := resolveRegistryHashes(t.Context(), testRegistry(), newTestResolver(srv))
	if len(results) != 2 {
		t.Fatalf("results = %d", len(results))
	}
	a := results[0]
	if a.Error != "" {
		t.Fatalf("entry a error = %s", a.Error)
	}
	if a.ID != "a" || a.SHA256 != testOID || a.Source != "lfs" {
		t.Fatalf("entry a = %+v", a)
	}
	if a.Repo != "owner/a" || a.Revision != "main" || a.Path != "model.onnx" {
		t.Fatalf("entry a target = %+v", a)
	}
	if len(a.ExtraFiles) != 1 || a.ExtraFiles[0] != "model.onnx.data" {
		t.Fatalf("entry a extraFiles = %v", a.ExtraFiles)
	}
	b := results[1]
	if b.Error == "" || !strings.Contains(b.Error, "not found") {
		t.Fatalf("entry b error = %q", b.Error)
	}
}

func writeTestRegistry(t *testing.T) string {
	t.Helper()
	reg := testRegistry()
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestModelsHashWriteAndCheck(t *testing.T) {
	srv := newHFServer(t)
	path := writeTestRegistry(t)
	ctx := context.Background()

	if err := runModelsHash(ctx, []string{"--registry", path, "--check"}); err == nil {
		t.Fatal("--check must fail while sha256 values are empty")
	}

	if err := runModelsHash(ctx, []string{"--registry", path, "--api-base", srv.URL, "--write"}); err != nil {
		t.Fatalf("--write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := models.ParseRegistry(data)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := reg.Lookup("a")
	if !ok || a.SHA256 != testOID {
		t.Fatalf("entry a = %+v %v", a, ok)
	}
	if len(a.ExtraFiles) != 1 || a.ExtraFiles[0] != "model.onnx.data" {
		t.Fatalf("entry a extraFiles = %v", a.ExtraFiles)
	}
	// Entry b could not be resolved and must keep its empty hash.
	b, _ := reg.Lookup("b")
	if b.SHA256 != "" {
		t.Fatalf("entry b sha256 = %q, want empty", b.SHA256)
	}
	// --check still fails because b is unresolved.
	if err := runModelsHash(ctx, []string{"--registry", path, "--check"}); err == nil {
		t.Fatal("--check must fail while one entry is unresolved")
	}
	// Fill b manually and the check passes.
	patches := map[string]models.EntryPatch{"b": {SHA256: testOID2}}
	if _, err := models.PatchRegistryFile(path, patches); err != nil {
		t.Fatal(err)
	}
	if err := runModelsHash(ctx, []string{"--registry", path, "--check"}); err != nil {
		t.Fatalf("--check after filling hashes: %v", err)
	}

	// --json prints the resolution mapping.
	if err := runModelsHash(ctx, []string{"--registry", path, "--api-base", srv.URL, "--json"}); err != nil {
		t.Fatalf("--json: %v", err)
	}
}

func TestCheckRegistryHashes(t *testing.T) {
	cases := []struct {
		name    string
		sha     string
		wantErr string
	}{
		{"valid", testOID, ""},
		{"empty", "", "without a valid sha256"},
		{"malformed", "abc", "without a valid sha256"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := &models.Registry{Version: 2, Models: []models.Entry{{ID: "x", SHA256: tc.sha}}}
			err := checkRegistryHashes(reg)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("checkRegistryHashes = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "x") {
				t.Fatalf("err = %v, want it to name the failing id", err)
			}
		})
	}
	if err := checkRegistryHashes(&models.Registry{}); err != nil {
		t.Fatalf("empty registry should pass the check: %v", err)
	}
}

func TestLocateRegistry(t *testing.T) {
	explicit := filepath.Join(t.TempDir(), "custom.json")
	if got, err := locateRegistry(explicit); err != nil || got != explicit {
		t.Fatalf("locateRegistry(explicit) = %q %v", got, err)
	}
	// Auto-detect resolves the repo registry through runtime.Caller.
	got, err := locateRegistry("")
	if err != nil {
		t.Fatalf("locateRegistry(auto): %v", err)
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.ParseRegistry(data); err != nil {
		t.Fatalf("auto-detected registry is not valid: %v", err)
	}
}
