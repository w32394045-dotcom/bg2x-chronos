package models

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func validSidecar() Sidecar {
	s := Sidecar{
		Inputs:      []SidecarInput{{Name: "input", Source: SourceImage}},
		Outputs:     []SidecarOutput{{Name: "output", Scale: 4, Crop: 0}},
		Preprocess:  Preprocess{Norm: Norm01, Channels: 3},
		Constraints: Constraints{TileAlign: 4, Batch: 1, DType: DTypeFP32},
		Meta:        Meta{License: "MIT", Arch: "rrdb", Notes: "test"},
	}
	s.ApplyDefaults()
	return s
}

func TestSidecarJSONKeys(t *testing.T) {
	sc := validSidecar()
	data, err := json.Marshal(sc)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"inputs", "outputs", "preprocess", "constraints", "meta"} {
		if _, ok := top[key]; !ok {
			t.Errorf("missing top-level key %q in %s", key, data)
		}
	}
	var pre map[string]json.RawMessage
	if err := json.Unmarshal(top["preprocess"], &pre); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"norm", "channels"} {
		if _, ok := pre[key]; !ok {
			t.Errorf("missing preprocess key %q", key)
		}
	}
	var con map[string]json.RawMessage
	if err := json.Unmarshal(top["constraints"], &con); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"tileAlign", "fixedSize", "batch", "dtype"} {
		if _, ok := con[key]; !ok {
			t.Errorf("missing constraints key %q", key)
		}
	}
	var out []map[string]json.RawMessage
	if err := json.Unmarshal(top["outputs"], &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("outputs = %d", len(out))
	}
	for _, key := range []string{"name", "scale", "crop"} {
		if _, ok := out[0][key]; !ok {
			t.Errorf("missing output key %q", key)
		}
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(top["meta"], &meta); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"license", "arch", "notes"} {
		if _, ok := meta[key]; !ok {
			t.Errorf("missing meta key %q", key)
		}
	}
}

func TestSidecarRoundTrip(t *testing.T) {
	sc := Sidecar{
		Inputs: []SidecarInput{
			{Name: "image", Source: SourceImage},
			{Name: "scale", Source: "constant:1.0"},
		},
		Outputs: []SidecarOutput{{Name: "out", Scale: 4, Crop: 2}},
		Preprocess: Preprocess{
			Norm:     NormNeg11,
			Channels: 3,
			Mean:     []float64{0.5, 0.5, 0.5},
			Std:      []float64{0.5, 0.5, 0.5},
		},
		Constraints: Constraints{TileAlign: 4, FixedSize: 64, Batch: 2, DType: DTypeFP16},
		Meta:        Meta{License: "MIT", Arch: "rrdb", Notes: "note"},
	}
	path := filepath.Join(t.TempDir(), "model.json")
	if err := sc.Write(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Fatal("sidecar does not end with a newline")
	}
	if !strings.Contains(string(raw), "\n  \"inputs\"") {
		t.Fatalf("sidecar is not 2-space indented:\n%s", raw)
	}
	back, err := ParseSidecar(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sc, back) {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", back, sc)
	}
	if back.DefaultScale() != 4 {
		t.Fatalf("DefaultScale = %d", back.DefaultScale())
	}
}

func TestSidecarDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.json")
	writeTestFile(t, path, []byte("{}"))
	sc, err := ParseSidecar(path)
	if err != nil {
		t.Fatal(err)
	}
	if sc.Preprocess.Channels != DefaultChannels {
		t.Errorf("channels = %d", sc.Preprocess.Channels)
	}
	if sc.Preprocess.Norm != DefaultNorm {
		t.Errorf("norm = %q", sc.Preprocess.Norm)
	}
	if sc.Constraints.Batch != DefaultBatch {
		t.Errorf("batch = %d", sc.Constraints.Batch)
	}
	if sc.Constraints.TileAlign != DefaultTileAlign {
		t.Errorf("tileAlign = %d, want %d", sc.Constraints.TileAlign, DefaultTileAlign)
	}
	if sc.Constraints.DType != DTypeFP32 {
		t.Errorf("dtype = %q", sc.Constraints.DType)
	}
	if sc.Inputs == nil || sc.Outputs == nil {
		t.Error("inputs/outputs should be non-nil after defaults")
	}
	if err := sc.Validate(); err != nil {
		t.Errorf("default sidecar invalid: %v", err)
	}
	if got := sc.DefaultScale(); got != 1 {
		t.Errorf("DefaultScale = %d, want 1", got)
	}
	if got := (Sidecar{}).DefaultScale(); got != 1 {
		t.Errorf("zero sidecar DefaultScale = %d, want 1", got)
	}
}

func TestSidecarWriteAppliesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.json")
	sc := Sidecar{Outputs: []SidecarOutput{{Name: "out", Scale: 2}}}
	if err := sc.Write(path); err != nil {
		t.Fatal(err)
	}
	back, err := ParseSidecar(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.Preprocess.Norm != Norm01 || back.Preprocess.Channels != 3 ||
		back.Constraints.Batch != 1 || back.Constraints.DType != DTypeFP32 {
		t.Fatalf("defaults not written: %+v", back)
	}
}

func TestSidecarValidateRejects(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Sidecar)
		wantErr string
	}{
		{"scale zero", func(s *Sidecar) { s.Outputs = []SidecarOutput{{Name: "o", Scale: 0}} }, "scale must be > 0"},
		{"scale negative", func(s *Sidecar) { s.Outputs[0].Scale = -2 }, "scale must be > 0"},
		{"crop negative", func(s *Sidecar) { s.Outputs[0].Crop = -1 }, "crop must be >= 0"},
		{"channels zero", func(s *Sidecar) { s.Preprocess.Channels = 0 }, "channels"},
		{"channels two", func(s *Sidecar) { s.Preprocess.Channels = 2 }, "channels"},
		{"channels five", func(s *Sidecar) { s.Preprocess.Channels = 5 }, "channels"},
		{"norm unknown", func(s *Sidecar) { s.Preprocess.Norm = "0-2" }, "norm"},
		{"tileAlign negative", func(s *Sidecar) { s.Constraints.TileAlign = -8 }, "tileAlign"},
		{"fixedSize negative", func(s *Sidecar) { s.Constraints.FixedSize = -1 }, "fixedSize"},
		{"batch zero", func(s *Sidecar) { s.Constraints.Batch = 0 }, "batch"},
		{"dtype unknown", func(s *Sidecar) { s.Constraints.DType = "int8" }, "dtype"},
		{"input source empty", func(s *Sidecar) { s.Inputs = []SidecarInput{{Name: "x"}} }, "source"},
		{"input source unknown", func(s *Sidecar) { s.Inputs = []SidecarInput{{Name: "x", Source: "audio"}} }, "source"},
		{"input constant bad", func(s *Sidecar) { s.Inputs = []SidecarInput{{Name: "x", Source: "constant:abc"}} }, "source"},
		{"input constant empty", func(s *Sidecar) { s.Inputs = []SidecarInput{{Name: "x", Source: "constant:"}} }, "source"},
		{"mean length", func(s *Sidecar) { s.Preprocess.Mean = []float64{1, 2} }, "mean"},
		{"std length", func(s *Sidecar) { s.Preprocess.Std = []float64{1, 2, 3, 4} }, "std"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := validSidecar()
			tc.mutate(&sc)
			err := sc.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestSidecarValidateAccepts(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Sidecar)
	}{
		{"defaults", func(s *Sidecar) {}},
		{"channels one", func(s *Sidecar) {
			s.Preprocess.Channels = 1
			s.Preprocess.Mean = []float64{0.5}
			s.Preprocess.Std = []float64{0.5}
		}},
		{"channels four", func(s *Sidecar) { s.Preprocess.Channels = 4; s.Preprocess.Mean = nil; s.Preprocess.Std = nil }},
		{"norm 0-255", func(s *Sidecar) { s.Preprocess.Norm = Norm0255 }},
		{"norm -1-1", func(s *Sidecar) { s.Preprocess.Norm = NormNeg11 }},
		{"fp16", func(s *Sidecar) { s.Constraints.DType = DTypeFP16 }},
		{"constant int", func(s *Sidecar) { s.Inputs = []SidecarInput{{Name: "scale", Source: "constant:2"}} }},
		{"constant negative", func(s *Sidecar) { s.Inputs = []SidecarInput{{Name: "scale", Source: "constant:-1.5"}} }},
		{"no outputs", func(s *Sidecar) { s.Outputs = nil }},
		{"crop positive", func(s *Sidecar) { s.Outputs[0].Crop = 8 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := validSidecar()
			tc.mutate(&sc)
			if err := sc.Validate(); err != nil {
				t.Fatalf("Validate() = %v", err)
			}
		})
	}
}

func TestSidecarParseErrors(t *testing.T) {
	if _, err := ParseSidecar(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("expected error for missing sidecar")
	}
	path := filepath.Join(t.TempDir(), "model.json")
	writeTestFile(t, path, []byte("{not json"))
	if _, err := ParseSidecar(path); err == nil {
		t.Fatal("expected error for malformed sidecar")
	}
}

func TestSidecarWriteRejectsInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.json")
	sc := validSidecar()
	sc.Outputs[0].Scale = 0
	if err := sc.Write(path); err == nil {
		t.Fatal("Write() accepted an invalid sidecar")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("invalid sidecar was written (err=%v)", err)
	}
}

func TestSidecarForEntry(t *testing.T) {
	e := Entry{
		ID:          "fake-x4",
		Name:        "Fake x4",
		Scale:       4,
		Description: "note",
		URL:         "https://example.invalid/fake.onnx",
		Arch:        "swin2sr",
		License:     "Apache-2.0",
		TileAlign:   8,
		Norm:        NormNeg11,
	}
	sc := SidecarForEntry(e)
	if err := sc.Validate(); err != nil {
		t.Fatalf("derived sidecar invalid: %v", err)
	}
	if sc.DefaultScale() != 4 {
		t.Fatalf("scale = %d", sc.DefaultScale())
	}
	if sc.Meta.Arch != "swin2sr" || sc.Meta.License != "Apache-2.0" || sc.Meta.Notes != "note" {
		t.Fatalf("meta = %+v", sc.Meta)
	}
	if sc.Constraints.TileAlign != 8 || sc.Constraints.Batch != 1 {
		t.Fatalf("constraints = %+v", sc.Constraints)
	}
	if sc.Preprocess.Norm != NormNeg11 || sc.Preprocess.Channels != 3 {
		t.Fatalf("preprocess = %+v", sc.Preprocess)
	}
	if len(sc.Inputs) != 1 || sc.Inputs[0].Source != SourceImage {
		t.Fatalf("inputs = %+v", sc.Inputs)
	}

	zero := SidecarForEntry(Entry{ID: "x", URL: "https://example.invalid/x.onnx"})
	if zero.DefaultScale() != 1 {
		t.Fatalf("scale for scale-less entry = %d, want 1", zero.DefaultScale())
	}
	if zero.Constraints.TileAlign != DefaultTileAlign {
		t.Fatalf("tileAlign for tile-less entry = %d, want %d", zero.Constraints.TileAlign, DefaultTileAlign)
	}
	if err := zero.Validate(); err != nil {
		t.Fatalf("scale-less sidecar invalid: %v", err)
	}
}

func TestSidecarDefaultScaleFirstValidOutput(t *testing.T) {
	sc := Sidecar{Outputs: []SidecarOutput{
		{Name: "a", Scale: 0},
		{Name: "b", Scale: 3},
		{Name: "c", Scale: 4},
	}}
	if got := sc.DefaultScale(); got != 3 {
		t.Fatalf("DefaultScale = %d, want 3", got)
	}
}
