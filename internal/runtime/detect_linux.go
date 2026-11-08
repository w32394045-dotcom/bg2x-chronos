//go:build linux

package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// nvidiaPresent reports whether the machine looks like it has an NVIDIA GPU and
// driver. It only gates whether we OFFER the CUDA download: a false positive
// costs a download, and Verify() plus CPU fallback keep it safe.
func nvidiaPresent() bool {
	for _, path := range []string{"/dev/nvidia0", "/proc/driver/nvidia/version"} {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	if _, err := exec.LookPath("nvidia-smi"); err == nil {
		return true
	}
	for _, path := range []string{"/usr/lib/x86_64-linux-gnu/libcuda.so.1", "/usr/lib64/libcuda.so.1"} {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

// intelPresent reports whether the machine has an Intel GPU (or an Intel CPU,
// which the OpenVINO CPU plugin accelerates). It gates the OpenVINO download,
// which is the Linux iGPU path.
func intelPresent() bool {
	matches, _ := filepath.Glob("/sys/class/drm/card*/device/vendor")
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(strings.ToLower(string(data)), "0x8086") {
			return true
		}
	}
	if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		if strings.Contains(string(data), "GenuineIntel") {
			return true
		}
	}
	return false
}
