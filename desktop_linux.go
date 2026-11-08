//go:build linux

package main

import "os"

// desktopAvailable reports whether an interactive display session exists. On a
// headless server/SSH session a GTK window cannot be created, so the app falls
// back to browser mode (which works over SSH port forwarding too).
func desktopAvailable() bool {
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("XDG_SESSION_TYPE") == "wayland"
}
