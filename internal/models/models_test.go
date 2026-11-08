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

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func fakeRegistry(url string) *Registry {
	return &Registry{
		Version: 2,
		Updated: "2026-11-08",
		Models: []Entry{{
			ID:          "fake-x2",
			Name:        "Fake x2",
			Scale:       2,
			Kind:        "general",
			Description: "test model",
			SizeMB:      0.1,
			URL:         url,
			SHA256:      sha256Hex(fakeModel),
			Arch:        "rrdb",
			License:     "MIT",
			TileAlign:   2,
			Norm:        Norm01,
		}},
	}
}

func newTestStore(t *testing.T, dir string, reg *Registry) *Store {
	t.Helper()
	store, err := NewStore(dir, reg)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read dir %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestEmbeddedRegistryV2(t *testing.T) {
	reg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if reg.Version != 2 {
		t.Fatalf("registry version = %d, want 2", reg.Version)
	}
	if len(reg.Models) != 10 {
		t.Fatalf("models = %d, want 10", len(reg.Models))
	}
	ids := make(map[string]bool, len(reg.Models))
	for _, e := range reg.Models {
		if e.ID == "" || e.Name == "" || e.URL == "" {
			t.Errorf("entry has empty id/name/url: %+v", e)
		}
		if e.Scale <= 0 {
			t.Errorf("%s: scale = %d", e.ID, e.Scale)
		}
		if ids[e.ID] {
			t.Errorf("duplicate id %q", e.ID)
		}
		ids[e.ID] = true
		for _, field := range []struct {
			name  string
			value string
		}{
			{"arch", e.Arch},
			{"license", e.License},
			{"norm", e.Norm},
		} {
			if field.value == "" {
				t.Errorf("%s: registry v2 field %q is empty", e.ID, field.name)
			}
		}
		if e.TileAlign < 0 {
			t.Errorf("%s: negative tileAlign %d", e.ID, e.TileAlign)
		}
		if !ValidSHA256(e.SHA256) {
			t.Errorf("%s: sha256 = %q is not a valid digest", e.ID, e.SHA256)
		}
	}
	// One digest cross-checked against the actual bytes of the upstream file.
	e, ok := reg.Lookup("real-esrgan-animevideov3")
	if !ok {
		t.Fatal("real-esrgan-animevideov3 missing")
	}
	const want = "78baa685a1a92cac6e14ab2af2a8ad0ef56fa48629563b1414e3a7acc54d86e2"
	if e.SHA256 != want {
		t.Fatalf("%s sha256 = %s, want %s", e.ID, e.SHA256, want)
	}
	if _, ok := reg.Lookup("nope"); ok {
		t.Fatal("lookup of unknown model succeeded")
	}
}

func TestModelPaths(t *testing.T) {
	store := newTestStore(t, t.TempDir(), fakeRegistry("https://example.invalid/fake.onnx"))
	e := store.Registry().Models[0]
	if got, want := store.ModelDir(e), filepath.Join(store.Dir(), "fake-x2"); got != want {
		t.Fatalf("ModelDir = %q, want %q", got, want)
	}
	if got, want := store.ModelFile(e), filepath.Join(store.ModelDir(e), PrimaryONNX); got != want {
		t.Fatalf("ModelFile = %q, want %q", got, want)
	}
	if got := store.PathFor(e); got != store.ModelFile(e) {
		t.Fatalf("PathFor alias = %q, want %q", got, store.ModelFile(e))
	}
	if got, want := store.SidecarPath(e), filepath.Join(store.ModelDir(e), SidecarFile); got != want {
		t.Fatalf("SidecarPath = %q, want %q", got, want)
	}
	xml := Entry{ID: "ir-x4", URL: "https://example.invalid/model.xml"}
	if got := PrimaryName(xml); got != PrimaryXML {
		t.Fatalf("PrimaryName(xml) = %q", got)
	}
	if got := PrimaryName(e); got != PrimaryONNX {
		t.Fatalf("PrimaryName(onnx) = %q", got)
	}
}

func TestListReportsInstalledState(t *testing.T) {
	dir := t.TempDir()
	reg := fakeRegistry("https://example.invalid/fake.onnx")
	store := newTestStore(t, dir, reg)
	list := store.List()
	if len(list) != 1 || list[0].Installed {
		t.Fatalf("list = %+v", list)
	}
	if list[0].Path != filepath.Join(dir, "fake-x2", PrimaryONNX) {
		t.Fatalf("path = %q", list[0].Path)
	}
	writeTestFile(t, store.ModelFile(reg.Models[0]), []byte(fakeModel))
	list = store.List()
	if !list[0].Installed {
		t.Fatalf("expected installed, list = %+v", list)
	}
	path, installed, err := store.Resolve("fake-x2")
	if err != nil || !installed || path != store.ModelFile(reg.Models[0]) {
		t.Fatalf("resolve = %q %v %v", path, installed, err)
	}
	if _, _, err := store.Resolve("ghost"); err == nil {
		t.Fatal("expected error for unknown model")
	}
}

func TestLegacyMigration(t *testing.T) {
	const id = "fake-x2"
	cases := []struct {
		name        string
		url         string
		files       map[string]string
		wantPrimary string
		wantExtra   string
	}{
		{
			name:        "flat onnx",
			url:         "https://example.invalid/fake.onnx",
			files:       map[string]string{id + ".onnx": fakeModel},
			wantPrimary: PrimaryONNX,
		},
		{
			name:        "flat onnx with external data",
			url:         "https://example.invalid/fake.onnx",
			files:       map[string]string{id + ".onnx": fakeModel, id + ".onnx.data": "external-data"},
			wantPrimary: PrimaryONNX,
			wantExtra:   PrimaryONNX + DataSuffix,
		},
		{
			name:        "flat openvino ir",
			url:         "https://example.invalid/fake.xml",
			files:       map[string]string{id + ".xml": "<net/>"},
			wantPrimary: PrimaryXML,
		},
		{
			name:        "flat openvino ir with bin",
			url:         "https://example.invalid/fake.xml",
			files:       map[string]string{id + ".xml": "<net/>", id + ".xml.bin": "weights"},
			wantPrimary: PrimaryXML,
			wantExtra:   PrimaryXML + ".bin",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tc.files {
				writeTestFile(t, filepath.Join(dir, name), []byte(content))
			}
			store := newTestStore(t, dir, fakeRegistry(tc.url))

			list := store.List()
			if len(list) != 1 || !list[0].Installed {
				t.Fatalf("list after migration = %+v", list)
			}
			gotPrimary := filepath.Join(dir, id, tc.wantPrimary)
			if list[0].Path != gotPrimary {
				t.Fatalf("path = %q, want %q", list[0].Path, gotPrimary)
			}
			if want := tc.files[id+"."+strings.TrimPrefix(tc.wantPrimary, "model.")]; want != "" {
				if got := readTestFile(t, gotPrimary); got != want {
					t.Fatalf("primary content = %q, want %q", got, want)
				}
			}
			if tc.wantExtra != "" {
				extra := filepath.Join(dir, id, tc.wantExtra)
				legacyName := id + "." + strings.TrimPrefix(tc.wantExtra, "model.")
				if got := readTestFile(t, extra); got != tc.files[legacyName] {
					t.Fatalf("companion content = %q, want %q", got, tc.files[legacyName])
				}
			}
			// Legacy flat files are gone.
			for name := range tc.files {
				if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
					t.Fatalf("legacy file %s still exists (err=%v)", name, err)
				}
			}
			// Sidecar was derived from the registry.
			sc, err := ParseSidecar(store.SidecarPath(store.Registry().Models[0]))
			if err != nil {
				t.Fatalf("sidecar: %v", err)
			}
			if sc.DefaultScale() != 2 || sc.Meta.Arch != "rrdb" || sc.Constraints.TileAlign != 2 {
				t.Fatalf("sidecar = %+v", sc)
			}
			// Migration is recorded and idempotent.
			migs := store.Migrations()
			if len(migs) != 1 || migs[0].ID != id || migs[0].Err != nil {
				t.Fatalf("migrations = %+v", migs)
			}
			store.List()
			if got := len(store.Migrations()); got != 1 {
				t.Fatalf("migrations after second list = %d, want 1", got)
			}
			store.MigrateAll()
			if got := len(store.Migrations()); got != 1 {
				t.Fatalf("migrations after MigrateAll = %d, want 1", got)
			}
		})
	}
}

func TestMigrationDegradesWhenNotWritable(t *testing.T) {
	dir := t.TempDir()
	reg := fakeRegistry("https://example.invalid/fake.onnx")
	writeTestFile(t, filepath.Join(dir, "fake-x2.onnx"), []byte(fakeModel))
	// Occupy the destination directory path with a regular file: mkdir fails.
	writeTestFile(t, filepath.Join(dir, "fake-x2"), []byte("blocker"))
	store := newTestStore(t, dir, reg)

	list := store.List()
	if len(list) != 1 || list[0].Installed {
		t.Fatalf("list = %+v, want not installed", list)
	}
	migs := store.Migrations()
	if len(migs) != 1 || migs[0].Err == nil {
		t.Fatalf("migrations = %+v, want one failed record", migs)
	}
	path, installed, err := store.Resolve("fake-x2")
	if err != nil {
		t.Fatalf("Resolve returned an error on failed migration: %v", err)
	}
	if installed || path != store.ModelFile(reg.Models[0]) {
		t.Fatalf("resolve = %q %v", path, installed)
	}
}

func TestMigrateAll(t *testing.T) {
	dir := t.TempDir()
	reg := fakeRegistry("https://example.invalid/fake.onnx")
	writeTestFile(t, filepath.Join(dir, "fake-x2.onnx"), []byte(fakeModel))
	store := newTestStore(t, dir, reg)
	migs := store.MigrateAll()
	if len(migs) != 1 || migs[0].Err != nil {
		t.Fatalf("migrate all = %+v", migs)
	}
	if got := store.MigrateAll(); len(got) != 0 {
		t.Fatalf("second MigrateAll = %+v, want empty", got)
	}
	if !isInstalledFile(filepath.Join(dir, "fake-x2", PrimaryONNX)) {
		t.Fatal("model not migrated")
	}
}

func TestPullIntoV2Layout(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Length", strconv.Itoa(len(fakeModel)))
		_, _ = w.Write([]byte(fakeModel))
	}))
	defer srv.Close()

	reg := fakeRegistry(srv.URL)
	dir := t.TempDir()
	store := newTestStore(t, dir, reg)
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
	wantPath := filepath.Join(dir, "fake-x2", PrimaryONNX)
	if path != wantPath {
		t.Fatalf("pull path = %q, want %q", path, wantPath)
	}
	if got := readTestFile(t, path); got != fakeModel {
		t.Fatalf("content = %q", got)
	}
	if progressCalls == 0 {
		t.Fatal("no progress reported")
	}
	// Sidecar was written next to the weights.
	sc, err := ParseSidecar(store.SidecarPath(reg.Models[0]))
	if err != nil {
		t.Fatal(err)
	}
	if sc.DefaultScale() != 2 || sc.Meta.License != "MIT" || sc.Meta.Arch != "rrdb" {
		t.Fatalf("sidecar = %+v", sc)
	}
	// No temporary download files are left behind.
	for _, name := range dirNames(t, store.ModelDir(reg.Models[0])) {
		if strings.HasPrefix(name, ".chronos-") {
			t.Fatalf("leftover temp file %q", name)
		}
	}
	// Second pull must hit the cache, not the server.
	if _, err := store.Pull(t.Context(), "fake-x2", nil); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("server hits = %d, want 1", hits)
	}
}

func TestPullHashMismatchCleansUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("tampered"))
	}))
	defer srv.Close()
	reg := fakeRegistry(srv.URL)
	dir := t.TempDir()
	store := newTestStore(t, dir, reg)
	_, err := store.Pull(t.Context(), "fake-x2", nil)
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("error = %v, want sha256 mismatch", err)
	}
	modelDir := store.ModelDir(reg.Models[0])
	if _, statErr := os.Stat(store.ModelFile(reg.Models[0])); !os.IsNotExist(statErr) {
		t.Fatal("corrupt download must not be installed")
	}
	for _, name := range dirNames(t, modelDir) {
		if strings.HasPrefix(name, ".chronos-") {
			t.Fatalf("leftover temp file %q", name)
		}
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
	reg.Models[0].FallbackURLs = []string{good.URL}
	store := newTestStore(t, t.TempDir(), reg)
	path, err := store.Pull(t.Context(), "fake-x2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, path); got != fakeModel {
		t.Fatalf("content = %q", got)
	}
}

func TestPullCompanionFiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/weights/primary.onnx":
			_, _ = w.Write([]byte(fakeModel))
		case "/weights/primary.onnx.data":
			_, _ = w.Write([]byte("external-data"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	reg := fakeRegistry(srv.URL + "/weights/primary.onnx")
	reg.Models[0].ExtraFiles = []string{PrimaryONNX + DataSuffix}
	store := newTestStore(t, t.TempDir(), reg)
	path, err := store.Pull(t.Context(), "fake-x2", nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := store.ModelDir(reg.Models[0])
	companion := filepath.Join(dir, PrimaryONNX+DataSuffix)
	if got := readTestFile(t, companion); got != "external-data" {
		t.Fatalf("companion content = %q", got)
	}
	if got := readTestFile(t, path); got != fakeModel {
		t.Fatalf("content = %q", got)
	}
}

func TestImportCopiesLocalFile(t *testing.T) {
	dir := t.TempDir()
	reg := fakeRegistry("https://example.invalid/fake.onnx")
	store := newTestStore(t, dir, reg)
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "local.onnx")
	writeTestFile(t, src, []byte(fakeModel))
	writeTestFile(t, src+DataSuffix, []byte("external-data"))

	dest, err := store.Import("fake-x2", src)
	if err != nil {
		t.Fatal(err)
	}
	wantDest := filepath.Join(dir, "fake-x2", PrimaryONNX)
	if dest != wantDest {
		t.Fatalf("dest = %q, want %q", dest, wantDest)
	}
	if got := readTestFile(t, dest); got != fakeModel {
		t.Fatalf("content = %q", got)
	}
	if got := readTestFile(t, dest+DataSuffix); got != "external-data" {
		t.Fatalf("companion content = %q", got)
	}
	sc, err := ParseSidecar(store.SidecarPath(reg.Models[0]))
	if err != nil {
		t.Fatalf("sidecar: %v", err)
	}
	if sc.DefaultScale() != 2 || sc.Meta.Arch != "rrdb" {
		t.Fatalf("sidecar = %+v", sc)
	}
	if _, installed, _ := store.Resolve("fake-x2"); !installed {
		t.Fatal("imported model is not reported as installed")
	}
	if _, err := store.Import("ghost", src); err == nil {
		t.Fatal("expected error importing an unknown model")
	}
	if _, err := store.Pull(t.Context(), "ghost", nil); err == nil {
		t.Fatal("expected error pulling an unknown model")
	}
}

func TestExtraFileURL(t *testing.T) {
	cases := []struct {
		name    string
		base    string
		primary string
		extra   string
		want    string
	}{
		{"weights suffix", "https://host/a/model.onnx", PrimaryONNX, "model.onnx.data", "https://host/a/model.onnx.data"},
		{"other file", "https://host/a/model.onnx", PrimaryONNX, "other.bin", "https://host/a/other.bin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extraFileURL(tc.base, tc.primary, tc.extra); got != tc.want {
				t.Fatalf("extraFileURL = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidSHA256(t *testing.T) {
	good := sha256Hex("x")
	if !ValidSHA256(good) {
		t.Fatalf("ValidSHA256(%q) = false", good)
	}
	if !ValidSHA256(strings.ToUpper(good)) {
		t.Fatal("uppercase digest rejected")
	}
	cases := map[string]string{
		"empty":      "",
		"short":      good[:63],
		"long":       good + "0",
		"non hex":    strings.Repeat("z", 64),
		"whitespace": " " + good[:63],
	}
	for name, value := range cases {
		if ValidSHA256(value) {
			t.Errorf("%s: ValidSHA256(%q) = true", name, value)
		}
	}
}

func TestParseRegistryErrors(t *testing.T) {
	if _, err := ParseRegistry([]byte("{")); err == nil {
		t.Fatal("expected parse error")
	}
	if _, err := ParseRegistry([]byte(`{"version": 2, "models": []}`)); err == nil {
		t.Fatal("expected empty registry error")
	}
}
