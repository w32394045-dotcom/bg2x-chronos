package engine

import (
	"encoding/json"
	"testing"
)

func TestParseProbe(t *testing.T) {
	full := []byte(`{"cpus":16,"ort":{"available":true,"version":"1.24.4","providers":["CPU","DML"]},` +
		`"openvino":{"available":false},"sidecarFound":true,"tileAlign":8,"fixedSize":0,"scale":4,"warnings":["w"]}`)
	p, err := ParseProbe(full)
	if err != nil {
		t.Fatalf("ParseProbe: %v", err)
	}
	if p.CPUs != 16 || !p.ORT.Available || p.ORT.Version != "1.24.4" {
		t.Fatalf("probe = %+v", p)
	}
	if len(p.ORT.Providers) != 2 || p.ORT.Providers[1] != "DML" {
		t.Fatalf("providers = %v", p.ORT.Providers)
	}
	if !p.SidecarFound || p.TileAlign != 8 || p.Scale != 4 {
		t.Fatalf("model fields = %+v", p)
	}

	// Missing fields must not fail (older native library).
	minimal, err := ParseProbe([]byte(`{"ort":{"available":false}}`))
	if err != nil {
		t.Fatalf("minimal probe: %v", err)
	}
	if minimal.CPUs != 0 || minimal.TileAlign != 0 || minimal.ORT.Available {
		t.Fatalf("minimal = %+v", minimal)
	}

	// Empty buffer is a valid "no data" report.
	if empty, err := ParseProbe(nil); err != nil || empty.CPUs != 0 {
		t.Fatalf("empty = %+v, %v", empty, err)
	}

	// Garbage must be an error, not a panic.
	if _, err := ParseProbe([]byte("not json")); err == nil {
		t.Fatal("expected an error for garbage input")
	}
}

func TestBackendFromDescribe(t *testing.T) {
	cases := []struct {
		in       string
		backend  string
		ep       string
	}{
		{"ort/CPU", "ort", "CPU"},
		{"ort/DML", "ort", "DML"},
		{"openvino/GPU.0", "openvino", "GPU.0"},
		{"stub/Lanczos3", "stub", "Lanczos3"},
		{"go-lanczos", "go-lanczos", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		be, ep := BackendFromDescribe(c.in)
		if be != c.backend || ep != c.ep {
			t.Errorf("BackendFromDescribe(%q) = (%q,%q), want (%q,%q)", c.in, be, ep, c.backend, c.ep)
		}
	}
}

func TestAvailableAccelerators(t *testing.T) {
	p := Probe{ORT: ProbeRuntimeInfo{Available: true, Providers: []string{"CPU", "DML", "cpu"}}}
	got := AvailableAccelerators(p)
	want := []string{"CPU", "DML"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("AvailableAccelerators = %v, want %v", got, want)
	}
	if accel := AvailableAccelerators(Probe{}); len(accel) != 1 || accel[0] != "CPU" {
		t.Fatalf("no runtime accelerators = %v", accel)
	}
}

func TestPickDevice(t *testing.T) {
	cases := []struct {
		name      string
		probe     Probe
		preferCPU bool
		device    string
		ep        string
		reason    bool // expect a non-empty reason
	}{
		{
			name:   "gpu provider available",
			probe:  Probe{ORT: ProbeRuntimeInfo{Available: true, Providers: []string{"CPU", "DML"}}},
			device: "GPU", ep: "DML",
		},
		{
			name:   "cpu only runtime degrades with a reason",
			probe:  Probe{ORT: ProbeRuntimeInfo{Available: true, Providers: []string{"CPU"}}},
			device: "CPU", ep: "CPU", reason: true,
		},
		{
			name:      "prefer cpu wins over gpu",
			probe:     Probe{ORT: ProbeRuntimeInfo{Available: true, Providers: []string{"CPU", "CUDA"}}},
			preferCPU: true,
			device:    "CPU", ep: "CPU", reason: true,
		},
		{
			name:   "no runtime at all",
			probe:  Probe{},
			device: "CPU", ep: "CPU", reason: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			device, ep, reason := PickDevice(c.probe, c.preferCPU)
			if device != c.device || ep != c.ep {
				t.Fatalf("PickDevice = (%q,%q), want (%q,%q)", device, ep, c.device, c.ep)
			}
			if c.reason && reason == "" {
				t.Fatal("expected a fallback reason")
			}
			if !c.reason && reason != "" {
				t.Fatalf("unexpected reason %q", reason)
			}
		})
	}
}

func TestIsGPUProvider(t *testing.T) {
	for _, ep := range []string{"DML", "dml", "CUDA", "ROCM", "GPU.0", "OpenVINO-GPU"} {
		if !IsGPUProvider(ep) {
			t.Errorf("IsGPUProvider(%q) = false", ep)
		}
	}
	for _, ep := range []string{"CPU", "Azure", ""} {
		if IsGPUProvider(ep) {
			t.Errorf("IsGPUProvider(%q) = true", ep)
		}
	}
}

// The probe JSON is produced by the native core; keep the Go shape in sync with
// the documented contract by round-tripping a representative document.
func TestProbeJSONRoundTrip(t *testing.T) {
	p, err := ParseProbe([]byte(`{"cpus":8,"ort":{"available":true,"version":"1.24.4","providers":["CPU"]},"openvino":{"available":false},"warnings":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	again, err := ParseProbe(data)
	if err != nil {
		t.Fatal(err)
	}
	if again.ORT.Version != "1.24.4" || again.CPUs != 8 {
		t.Fatalf("round trip = %+v", again)
	}
}
