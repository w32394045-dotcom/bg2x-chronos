package engine

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ModelTensor describes one model input/output tensor (docs/CONTRACTS.md v3).
type ModelTensor struct {
	Name string `json:"name"`
	// Dims uses -1 for dynamic dimensions.
	Dims  []int  `json:"dims"`
	Dtype string `json:"dtype"`
}

// ModelInspection is the native model report used by the import flow and the
// diagnostics dialog. It never contains image data, only graph metadata.
type ModelInspection struct {
	Available bool          `json:"available"`
	Scale     int           `json:"scale"` // 0 when it cannot be derived
	Inputs    []ModelTensor `json:"inputs"`
	Outputs   []ModelTensor `json:"outputs"`
	Warnings  []string      `json:"warnings"`
	Error     string        `json:"error"`
}

// ErrInspectUnavailable is returned when the binary has no native core, so the
// .onnx graph cannot be inspected at all.
var ErrInspectUnavailable = errors.New("chronos: model inspection unavailable")

// InspectModel reads an .onnx model's input/output metadata through the native
// core. It does not run inference and works without a chosen execution provider.
func InspectModel(path string) (ModelInspection, error) {
	if path == "" {
		return ModelInspection{}, CodedError(CodeParam, "inspect", "model path is empty")
	}
	return inspectModel(path)
}

// parseModelInspection is shared by the cgo and stub implementations.
func parseModelInspection(data []byte) (ModelInspection, error) {
	var out ModelInspection
	if err := json.Unmarshal(data, &out); err != nil {
		return ModelInspection{}, fmt.Errorf("chronos: parse model inspection: %w", err)
	}
	return out, nil
}
