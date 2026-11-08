package uiapp

import (
	"os"
	"path/filepath"
	"testing"
)

// isolateUserDirs points every per-user directory used by the core at temp
// dirs so tests never touch the developer's real config/cache.
func isolateUserDirs(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	for _, dir := range []string{
		filepath.Join(home, "AppData", "Roaming"),
		filepath.Join(home, "AppData", "Local"),
		filepath.Join(home, ".config"),
		filepath.Join(home, ".cache"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestModeAndAppInfo(t *testing.T) {
	isolateUserDirs(t)
	core := New(BuildInfo{Version: "9.9.9", Commit: "test", BuildTime: "now"})
	core.Startup()
	defer core.Shutdown()

	info := core.GetAppInfo()
	if info.Mode != ModeDesktop {
		t.Fatalf("default mode = %q, want desktop", info.Mode)
	}
	if info.BrowseSupported {
		t.Fatal("desktop mode must not advertise server-side browsing")
	}
	if info.Version != "9.9.9" || info.MaxConcurrency != 8 || info.Platform == "" {
		t.Fatalf("unexpected app info: %+v", info)
	}

	core.SetMode(ModeBrowser)
	info = core.GetAppInfo()
	if info.Mode != ModeBrowser {
		t.Fatalf("mode = %q, want browser", info.Mode)
	}
	if !info.BrowseSupported {
		t.Fatal("browser mode must advertise server-side browsing")
	}
	core.SetMode(ModeDesktop)
	if core.GetAppInfo().Mode != ModeDesktop {
		t.Fatal("mode did not switch back")
	}
}

func TestListDirectoryFiltersAndSorts(t *testing.T) {
	isolateUserDirs(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"z.png":       "x",
		"A.jpg":       "x",
		"notes.txt":   "x",
		".hidden.png": "x",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	core := New(BuildInfo{})
	defer core.Shutdown()

	listing, err := core.ListDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if listing.Path != dir {
		t.Fatalf("path = %q, want %q", listing.Path, dir)
	}
	if listing.Parent != filepath.Dir(dir) {
		t.Fatalf("parent = %q", listing.Parent)
	}
	if len(listing.Entries) != 3 {
		t.Fatalf("entries = %+v, want sub + A.jpg + z.png", listing.Entries)
	}
	if !listing.Entries[0].IsDir || listing.Entries[0].Name != "sub" {
		t.Fatalf("expected directories first, got %+v", listing.Entries[0])
	}
	if listing.Entries[1].Name != "A.jpg" || listing.Entries[2].Name != "z.png" {
		t.Fatalf("unexpected file order: %+v", listing.Entries)
	}
	if !listing.Entries[1].IsImage || listing.Entries[1].Size == 0 {
		t.Fatalf("image entry metadata = %+v", listing.Entries[1])
	}

	if _, err := core.ListDirectory(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("expected error for missing directory")
	}
}

func TestShutdownIsIdempotent(t *testing.T) {
	isolateUserDirs(t)
	core := New(BuildInfo{})
	core.Startup()
	core.Shutdown()
	core.Shutdown()
}
