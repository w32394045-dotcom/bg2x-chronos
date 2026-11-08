//go:build !windows && !linux

package main

// desktopAvailable is false on platforms without a Wails webview shell yet.
func desktopAvailable() bool { return false }
