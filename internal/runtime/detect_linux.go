//go:build linux

package runtime

import (
	"os"
	"os/exec"
	"strings"
)

// nvidiaPresent reports whether the machine looks like it has an NVIDIA GPU with
// a driver installed. It is a best-effort gate for offering the CUDA runtime
// download: a false positive only means the user may download something that
// falls back to CPU, never a crash.
func nvidiaPresent() bool {
	for _, path := range []string{"/dev/nvidia0", "/proc/driver/nvidia/version"} {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	if _, err := exec.LookPath("nvidia-smi"); err == nil {
		return true
	}
	// libcuda.so.1 may be installed without the device nodes (container).
	for _, path := range []string{"/usr/lib/x86_64-linux-gnu/libcuda.so.1", "/usr/lib64/libcuda.so.1"} {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

// cudaLibsHint explains what the CUDA runtime still needs from the system.
func cudaLibsHint() string {
	return "需要 NVIDIA 驱动与 CUDA 12 运行时库（libcudart/libcublas 等）"
}

var _ = strings.TrimSpace
