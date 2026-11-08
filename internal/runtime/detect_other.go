//go:build !linux

package runtime

// nvidiaPresent is only meaningful on Linux (Windows uses DirectML for every
// vendor, so the CUDA flavor is never offered there).
func nvidiaPresent() bool { return false }

// intelPresent is only meaningful on Linux (Windows Intel GPUs use DirectML).
func intelPresent() bool { return false }
