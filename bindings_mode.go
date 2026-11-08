//go:build bindings

package main

// wailsBindings is true when this binary is the throwaway one built by
// `wails generate bindings`. In that mode main() must delegate straight to
// wails.Run (which emits the bindings and exits) instead of trying to start a
// window or a browser server.
const wailsBindings = true
