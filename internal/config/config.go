// Package config persists the small amount of user state the desktop app needs
// (slider position, output dir, last model, inhibitor preference).
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// InhibitPrefs is the persisted sleep/lock preference.
type InhibitPrefs struct {
	Sleep       bool `json:"sleep"`
	Display     bool `json:"display"`
	Lock        bool `json:"lock"`
	AutoRelease bool `json:"autoRelease"`
}

// Settings is the whole persisted state.
type Settings struct {
	Concurrency int          `json:"concurrency"`
	OutputDir   string       `json:"outputDir"`
	LastModelID string       `json:"lastModelId"`
	Scale       int          `json:"scale"`
	TileSize    int          `json:"tileSize"`
	Overlap     int          `json:"overlap"`
	Threads     int          `json:"threads"`
	Fast        bool         `json:"fast"`
	Inhibit     InhibitPrefs `json:"inhibit"`
}

// Defaults returns the starting settings.
func Defaults() Settings {
	return Settings{
		Concurrency: 4,
		Scale:       4,
		TileSize:    256,
		Overlap:     16,
		Threads:     0,
	}
}

// DataDir is the per-user config directory for Chronos.
func DataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "Chronos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// DefaultOutputDir is ~/Pictures/Chronos when it can be resolved.
func DefaultOutputDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, "Pictures", "Chronos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	return dir
}

// Load reads settings from <dir>/settings.json, falling back to defaults.
func Load(dir string) (*Settings, error) {
	s := Defaults()
	path := filepath.Join(dir, "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &s, nil
		}
		return &s, fmt.Errorf("chronos: read settings: %w", err)
	}
	if err := json.Unmarshal(data, &s); err != nil {
		// Corrupt settings must never block startup.
		fallback := Defaults()
		return &fallback, fmt.Errorf("chronos: parse settings: %w", err)
	}
	if s.Concurrency < 1 || s.Concurrency > 8 {
		s.Concurrency = 4
	}
	if s.TileSize <= 0 {
		s.TileSize = 256
	}
	if s.Overlap < 0 || s.Overlap >= s.TileSize {
		s.Overlap = 16
	}
	if s.Scale < 1 || s.Scale > 8 {
		s.Scale = 4
	}
	return &s, nil
}

// Save writes settings atomically.
func (s *Settings) Save(dir string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "settings.json.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "settings.json"))
}
