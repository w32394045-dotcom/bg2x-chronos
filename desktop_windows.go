//go:build windows

package main

import (
	"os"
	"path/filepath"
)

// desktopAvailable reports whether the Microsoft Edge WebView2 runtime is
// installed; without it Wails cannot create a window, so we fall back to
// browser mode instead of showing a dead app.
func desktopAvailable() bool {
	candidates := []string{
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "EdgeWebView", "Application"),
		filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "EdgeWebView", "Application"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "EdgeWebView", "Application"),
	}
	for _, base := range candidates {
		if base == "" || base == "Microsoft"+string(filepath.Separator)+"EdgeWebView"+string(filepath.Separator)+"Application" {
			continue
		}
		if hasWebView2Runtime(base) {
			return true
		}
	}
	return false
}

func hasWebView2Runtime(base string) bool {
	versions, err := os.ReadDir(base)
	if err != nil {
		return false
	}
	for _, v := range versions {
		if !v.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(base, v.Name(), "msedgewebview2.exe")); err == nil {
			return true
		}
	}
	return false
}
