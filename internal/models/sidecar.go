package models

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Sidecar file name and schema constants (SR Contract v2).
const (
	// SidecarFile is the metadata file stored next to the weights:
	// models/<id>/model.json.
	SidecarFile = "model.json"

	// SourceImage marks an input tensor that receives the image tile.
	SourceImage = "image"
	// SourceConstantPrefix marks an input tensor fed with a constant value,
	// e.g. "constant:1.0".
	SourceConstantPrefix = "constant:"

	// Normalisation modes accepted by Preprocess.Norm.
	Norm01    = "0-1"
	Norm0255  = "0-255"
	NormNeg11 = "-1-1"

	// Element types accepted by Constraints.DType.
	DTypeFP32 = "fp32"
	DTypeFP16 = "fp16"

	// Defaults applied by ApplyDefaults and used when the sidecar omits a field.
	DefaultChannels  = 3
	DefaultNorm      = Norm01
	DefaultTileAlign = 1
	DefaultBatch     = 1
	DefaultDType     = DTypeFP32
)

// SidecarInput describes one model input tensor.
type SidecarInput struct {
	Name   string `json:"name"`
	Source string `json:"source"` // "image" or "constant:<value>"
}

// SidecarOutput describes one model output tensor.
type SidecarOutput struct {
	Name  string `json:"name"`
	Scale int    `json:"scale"` // required, > 0
	Crop  int    `json:"crop"`  // pixels cropped from each output border, default 0
}

// Preprocess describes how uint8 image data must be prepared for the model.
type Preprocess struct {
	Norm     string    `json:"norm"` // "0-1" | "0-255" | "-1-1"
	Channels int       `json:"channels"`
	Mean     []float64 `json:"mean,omitempty"`
	Std      []float64 `json:"std,omitempty"`
}

// Constraints describes tiling/runtime restrictions of the model.
type Constraints struct {
	TileAlign int    `json:"tileAlign"` // tile size must be a multiple of this (default 1 = no constraint)
	FixedSize int    `json:"fixedSize"` // >0 when the model only accepts that exact input size
	Batch     int    `json:"batch"`
	DType     string `json:"dtype"` // "fp32" | "fp16"
}

// Meta carries provenance/attribution data for the weights.
type Meta struct {
	License string `json:"license,omitempty"`
	Arch    string `json:"arch,omitempty"`
	Notes   string `json:"notes,omitempty"`
}

// Sidecar is the per-model metadata document stored as models/<id>/model.json.
type Sidecar struct {
	Inputs      []SidecarInput  `json:"inputs"`
	Outputs     []SidecarOutput `json:"outputs"`
	Preprocess  Preprocess      `json:"preprocess"`
	Constraints Constraints     `json:"constraints"`
	Meta        Meta            `json:"meta"`
}

// ApplyDefaults fills omitted fields in place: norm "0-1", channels 3, batch 1,
// dtype "fp32", and empty (non-nil) input/output lists.
func (s *Sidecar) ApplyDefaults() {
	if s == nil {
		return
	}
	if s.Preprocess.Norm == "" {
		s.Preprocess.Norm = DefaultNorm
	}
	if s.Preprocess.Channels == 0 {
		s.Preprocess.Channels = DefaultChannels
	}
	if s.Constraints.TileAlign == 0 {
		s.Constraints.TileAlign = DefaultTileAlign
	}
	if s.Constraints.Batch == 0 {
		s.Constraints.Batch = DefaultBatch
	}
	if s.Constraints.DType == "" {
		s.Constraints.DType = DefaultDType
	}
	if s.Inputs == nil {
		s.Inputs = []SidecarInput{}
	}
	if s.Outputs == nil {
		s.Outputs = []SidecarOutput{}
	}
}

// Validate rejects sidecars that cannot drive the engine: unknown norm, bad
// channel count, dtype, input source, negative crop/tileAlign/fixedSize,
// batch < 1 and outputs without a positive scale.
func (s *Sidecar) Validate() error {
	if s == nil {
		return fmt.Errorf("chronos: sidecar: nil")
	}
	for i, in := range s.Inputs {
		if !validInputSource(in.Source) {
			return fmt.Errorf("chronos: sidecar: input %d (%q): unsupported source %q", i, in.Name, in.Source)
		}
	}
	for i, out := range s.Outputs {
		if out.Scale <= 0 {
			return fmt.Errorf("chronos: sidecar: output %d (%q): scale must be > 0, got %d", i, out.Name, out.Scale)
		}
		if out.Crop < 0 {
			return fmt.Errorf("chronos: sidecar: output %d (%q): crop must be >= 0, got %d", i, out.Name, out.Crop)
		}
	}
	switch s.Preprocess.Channels {
	case 1, 3, 4:
	default:
		return fmt.Errorf("chronos: sidecar: preprocess.channels must be 1, 3 or 4, got %d", s.Preprocess.Channels)
	}
	switch s.Preprocess.Norm {
	case Norm01, Norm0255, NormNeg11:
	default:
		return fmt.Errorf("chronos: sidecar: preprocess.norm must be %q, %q or %q, got %q",
			Norm01, Norm0255, NormNeg11, s.Preprocess.Norm)
	}
	if n := len(s.Preprocess.Mean); n != 0 && n != s.Preprocess.Channels {
		return fmt.Errorf("chronos: sidecar: preprocess.mean has %d values, want %d", n, s.Preprocess.Channels)
	}
	if n := len(s.Preprocess.Std); n != 0 && n != s.Preprocess.Channels {
		return fmt.Errorf("chronos: sidecar: preprocess.std has %d values, want %d", n, s.Preprocess.Channels)
	}
	if s.Constraints.TileAlign < 0 {
		return fmt.Errorf("chronos: sidecar: constraints.tileAlign must be >= 0, got %d", s.Constraints.TileAlign)
	}
	if s.Constraints.FixedSize < 0 {
		return fmt.Errorf("chronos: sidecar: constraints.fixedSize must be >= 0, got %d", s.Constraints.FixedSize)
	}
	if s.Constraints.Batch < 1 {
		return fmt.Errorf("chronos: sidecar: constraints.batch must be >= 1, got %d", s.Constraints.Batch)
	}
	switch s.Constraints.DType {
	case DTypeFP32, DTypeFP16:
	default:
		return fmt.Errorf("chronos: sidecar: constraints.dtype must be %q or %q, got %q",
			DTypeFP32, DTypeFP16, s.Constraints.DType)
	}
	return nil
}

// DefaultScale returns the pixel scale of the first output that declares one,
// or 1 when the sidecar declares no usable scale.
func (s Sidecar) DefaultScale() int {
	for _, out := range s.Outputs {
		if out.Scale > 0 {
			return out.Scale
		}
	}
	return 1
}

// ParseSidecar reads and decodes a sidecar, applying defaults but not
// validating it (callers that drive the engine should call Validate).
func ParseSidecar(path string) (Sidecar, error) {
	var s Sidecar
	data, err := os.ReadFile(path)
	if err != nil {
		return s, fmt.Errorf("chronos: read sidecar: %w", err)
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return Sidecar{}, fmt.Errorf("chronos: parse sidecar %s: %w", path, err)
	}
	s.ApplyDefaults()
	return s, nil
}

// Write validates the sidecar (after applying defaults) and writes it
// atomically as 2-space indented JSON, creating parent directories as needed.
func (s *Sidecar) Write(path string) error {
	if s == nil {
		return fmt.Errorf("chronos: sidecar: nil")
	}
	s.ApplyDefaults()
	if err := s.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("chronos: encode sidecar: %w", err)
	}
	data = append(data, '\n')
	return WriteFileAtomic(path, data, 0o644)
}

// SidecarForEntry derives the default sidecar of a bundled registry entry.
func SidecarForEntry(e Entry) Sidecar {
	scale := e.Scale
	if scale <= 0 {
		scale = 1
	}
	norm := e.Norm
	if norm == "" {
		norm = DefaultNorm
	}
	s := Sidecar{
		Inputs:  []SidecarInput{{Name: "input", Source: SourceImage}},
		Outputs: []SidecarOutput{{Name: "output", Scale: scale, Crop: 0}},
		Preprocess: Preprocess{
			Norm:     norm,
			Channels: DefaultChannels,
		},
		Constraints: Constraints{
			TileAlign: e.TileAlign,
			Batch:     DefaultBatch,
			DType:     DefaultDType,
		},
		Meta: Meta{
			License: e.License,
			Arch:    e.Arch,
			Notes:   e.Description,
		},
	}
	s.ApplyDefaults()
	return s
}

// validInputSource accepts "image" or "constant:<float>".
func validInputSource(src string) bool {
	if src == SourceImage {
		return true
	}
	if !strings.HasPrefix(src, SourceConstantPrefix) {
		return false
	}
	value := strings.TrimSpace(strings.TrimPrefix(src, SourceConstantPrefix))
	if value == "" {
		return false
	}
	_, err := strconv.ParseFloat(value, 64)
	return err == nil
}
