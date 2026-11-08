package engine

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ProbeRuntimeInfo is one inference runtime's capability block in the native
// capability report (docs/CONTRACTS.md v3 §1.1).
type ProbeRuntimeInfo struct {
	Available bool     `json:"available"`
	Version   string   `json:"version"`
	Providers []string `json:"providers"`
}

// Probe mirrors the JSON document returned by chronos_backend_probe().
type Probe struct {
	CPUs         int              `json:"cpus"`
	ORT          ProbeRuntimeInfo `json:"ort"`
	OpenVINO     ProbeRuntimeInfo `json:"openvino"`
	SidecarFound bool             `json:"sidecarFound"`
	TileAlign    int              `json:"tileAlign"`
	FixedSize    int              `json:"fixedSize"`
	Scale        int              `json:"scale"`
	Warnings     []string         `json:"warnings"`
}

// ParseProbe decodes a capability report. Unknown fields are ignored and a
// report with missing sections still yields a usable value.
func ParseProbe(data []byte) (Probe, error) {
	var p Probe
	if len(strings.TrimSpace(string(data))) == 0 {
		return p, nil
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return Probe{}, fmt.Errorf("chronos: parse backend probe: %w", err)
	}
	return p, nil
}

// GetProbe returns the current native capability report. It is all zeroes when
// the binary has no native core or the runtime cannot be loaded.
func GetProbe() Probe {
	return runtimeProbe()
}

// BackendFromDescribe splits a chronos_session_describe() string such as
// "ort/CPU", "openvino/GPU.0" or "stub/Lanczos3" into backend and execution
// provider parts. The provider is empty when the describe string has no "/".
func BackendFromDescribe(describe string) (backend, ep string) {
	describe = strings.TrimSpace(describe)
	if describe == "" {
		return "", ""
	}
	parts := strings.SplitN(describe, "/", 2)
	backend = strings.ToLower(strings.TrimSpace(parts[0]))
	if len(parts) == 2 {
		ep = strings.TrimSpace(parts[1])
	}
	return backend, ep
}

// gpuProviders are the provider names that mean "this runs on a GPU".
var gpuProviders = []string{"DML", "CUDA", "ROCM", "TENSORRT", "MIGRAPHX"}

// IsGPUProvider reports whether an execution provider name implies GPU work.
func IsGPUProvider(ep string) bool {
	up := strings.ToUpper(strings.TrimSpace(ep))
	for _, g := range gpuProviders {
		if strings.HasPrefix(up, g) {
			return true
		}
	}
	return strings.Contains(up, "GPU")
}

// AvailableAccelerators lists the selectable accelerators, always including CPU.
// Entries look like "CPU", "DML", "CUDA", "OpenVINO".
func AvailableAccelerators(p Probe) []string {
	out := []string{"CPU"}
	seen := map[string]bool{"CPU": true}
	for _, provider := range p.ORT.Providers {
		name := strings.ToUpper(strings.TrimSpace(provider))
		if name == "" || name == "CPU" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	if p.OpenVINO.Available && !seen["OPENVINO"] {
		out = append(out, "OpenVINO")
	}
	return out
}

// PickDevice decides the device/EP to advertise given the runtime capabilities.
// preferCPU forces CPU. It returns the device ("GPU"|"CPU"), the EP name and a
// human readable reason for the CPU fallback (empty when nothing degraded).
func PickDevice(p Probe, preferCPU bool) (device, ep, reason string) {
	if preferCPU {
		return "CPU", "CPU", "已在设置中选择「仅 CPU」"
	}
	for _, provider := range p.ORT.Providers {
		if IsGPUProvider(provider) {
			return "GPU", strings.ToUpper(strings.TrimSpace(provider)), ""
		}
	}
	if !p.ORT.Available && !p.OpenVINO.Available {
		return "CPU", "CPU", "没有可用的推理运行时，使用纯 Go 重采样"
	}
	if p.ORT.Available {
		return "CPU", "CPU", "ONNX Runtime 运行时只带 CPU 执行后端"
	}
	return "CPU", "CPU", "未加载 ONNX Runtime，使用 OpenVINO/CPU"
}

// CPUFallbackReason summarises why we are not on a GPU ("" when on GPU).
func CPUFallbackReason(p Probe, info Info) string {
	if info.Device == "GPU" {
		return ""
	}
	if info.FallbackReason != "" {
		return info.FallbackReason
	}
	_, _, reason := PickDevice(p, false)
	return reason
}
