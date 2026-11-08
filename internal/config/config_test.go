package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Concurrency != 4 || s.TileSize != 256 || s.Overlap != 16 || s.Scale != 4 {
		t.Fatalf("defaults = %+v", s)
	}
	s.Concurrency = 6
	s.LastModelID = "real-esrgan-x4plus"
	s.Inhibit = InhibitPrefs{Sleep: true, Display: true, Lock: false, AutoRelease: true}
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Concurrency != 6 || got.LastModelID != "real-esrgan-x4plus" || !got.Inhibit.Sleep {
		t.Fatalf("loaded = %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json.tmp")); !os.IsNotExist(err) {
		t.Fatal("temporary settings file left behind")
	}
}

func TestCorruptSettingsFallBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err == nil {
		t.Fatal("expected a parse error to be reported")
	}
	if s.Concurrency != 4 || s.TileSize != 256 {
		t.Fatalf("fallback settings = %+v", s)
	}
}

func TestOutOfRangeValuesAreClamped(t *testing.T) {
	dir := t.TempDir()
	raw := `{"concurrency":99,"scale":0,"tileSize":0,"overlap":999}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Concurrency != 4 || s.Scale != 4 || s.TileSize != 256 || s.Overlap != 16 {
		t.Fatalf("clamped = %+v", s)
	}
}

func TestUIPreferencesDefaultsAndClamping(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Theme != ThemeDark || s.UIScale != 1.0 || s.AccelMode != AccelAuto {
		t.Fatalf("ui defaults = %+v", s)
	}

	// Legacy settings.json without the v0.3 fields must keep working.
	legacy := `{"concurrency":4,"scale":4,"tileSize":256,"overlap":16}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err = Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Theme != ThemeDark || s.UIScale != 1.0 || s.AccelMode != AccelAuto {
		t.Fatalf("legacy load = %+v", s)
	}

	raw := `{"theme":"neon","uiScale":9.5,"accelMode":"turbo"}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err = Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Theme != ThemeDark || s.UIScale != MaxUIScale || s.AccelMode != AccelAuto {
		t.Fatalf("clamped ui = %+v", s)
	}

	s.Theme = ThemeClaude
	s.UIScale = 0.3
	s.AccelMode = AccelCPU
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Theme != ThemeClaude || got.UIScale != MinUIScale || got.AccelMode != AccelCPU {
		t.Fatalf("round trip = %+v", got)
	}
}
