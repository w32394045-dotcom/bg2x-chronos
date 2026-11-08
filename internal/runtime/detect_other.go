//go:build !linux

package runtime

// nvidiaPresent is only meaningful on Linux; Windows uses DirectML.
func nvidiaPresent() bool { return false }

func cudaLibsHint() string { return "" }
