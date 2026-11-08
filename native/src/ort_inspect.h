/*
 * Project Chronos - ONNX model introspection for the external-model import flow.
 *
 * Creates a throwaway ONNX Runtime session (CPU), reads the model's inputs and
 * outputs (names, dtypes, shapes) and produces a small JSON report that the Go
 * layer turns into a sidecar / diagnostics document. No inference is executed.
 *
 * Everything works without a model-specific build flag: when the ONNX Runtime
 * library is absent the caller gets a clear error instead of a crash.
 */
#ifndef CHRONOS_ORT_INSPECT_H
#define CHRONOS_ORT_INSPECT_H

#include <string>

namespace chronos {

/* True when a model can be inspected (ONNX Runtime is loadable). */
bool can_inspect_models() noexcept;

/*
 * Builds the JSON report for one .onnx file.
 * Returns false and sets *error when the model cannot be read/loaded.
 * The report is always a complete JSON object, even on validation warnings:
 *   {"available":true,"scale":4,
 *    "inputs":[{"name":"input","dims":[-1,3,-1,-1],"dtype":"float32"}],
 *    "outputs":[...], "warnings":["..."], "error":""}
 */
bool inspect_model(const std::string& model_path, std::string* json, std::string* error);

}  // namespace chronos

#endif /* CHRONOS_ORT_INSPECT_H */
