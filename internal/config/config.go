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

// UI theme identifiers (docs/CONTRACTS.md v3, docs/PLAN-v0.3.md).
const (
	ThemeDark   = "dark"
	ThemeLight  = "light"
	ThemeClaude = "claude"
)

// Acceleration modes: auto tries a GPU execution provider and falls back to CPU;
// cpu forbids GPU providers entirely (docs/CONTRACTS.md v3 AccelStatus).
const (
	AccelAuto = "auto"
	AccelCPU  = "cpu"
)

// UI scale bounds (frontend zoom, 0.1 steps).
const (
	MinUIScale = 0.8
	MaxUIScale = 1.5
)

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
	Theme       string       `json:"theme"`     // dark | light | claude
	UIScale     float64      `json:"uiScale"`   // MinUIScale .. MaxUIScale
	AccelMode   string       `json:"accelMode"` // auto | cpu
	// GpuRuntimeDisabled records a GPU runtime whose verification failed, so
	// the app does not retry (and cannot crash on) it at every start.
	GpuRuntimeDisabled string `json:"gpuRuntimeDisabled,omitempty"`
}

// Defaults returns the starting settings.
func Defaults() Settings {
	return Settings{
		Concurrency: 4,
		Scale:       4,
		TileSize:    256,
		Overlap:     16,
		Threads:     0,
		Theme:       ThemeDark,
		UIScale:     1.0,
		AccelMode:   AccelAuto,
	}
}

// ValidTheme reports whether name is a known UI theme.
func ValidTheme(name string) bool {
	switch name {
	case ThemeDark, ThemeLight, ThemeClaude:
		return true
	}
	return false
}

// ValidAccelMode reports whether mode is a known acceleration mode.
func ValidAccelMode(mode string) bool {
	switch mode {
	case AccelAuto, AccelCPU:
		return true
	}
	return false
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
	// v0.3 UI/acceleration preferences: older settings.json files simply lack
	// these fields, so empty means "use the default".
	if !ValidTheme(s.Theme) {
		s.Theme = ThemeDark
	}
	if s.UIScale == 0 {
		s.UIScale = 1.0
	}
	if s.UIScale < MinUIScale {
		s.UIScale = MinUIScale
	}
	if s.UIScale > MaxUIScale {
		s.UIScale = MaxUIScale
	}
	if !ValidAccelMode(s.AccelMode) {
		s.AccelMode = AccelAuto
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
