/*
 * Project Chronos - ONNX model sidecar ("model.json") parser.
 *
 * A model directory may contain a sidecar file next to model.onnx that
 * describes how to feed and interpret the model. It is optional: when absent the
 * backend auto-detects the input/output names and shapes and uses defaults.
 *
 * Schema (kept identical to the Go side struct, do not rename fields):
 *
 *   {
 *     "inputs":  [{"name": "input", "source": "image"}],
 *     "outputs": [{"name": "", "scale": 4, "crop": 0}],
 *     "preprocess": {"norm": "0-1", "channels": 3, "mean": [], "std": []},
 *     "constraints": {"tileAlign": 1, "fixedSize": 0, "batch": 1, "dtype": "fp32"},
 *     "meta": {"license": "", "arch": "", "notes": ""}
 *   }
 *
 * Semantics implemented by backend_ort:
 *   - inputs[0].name empty  -> model input 0
 *   - outputs[0].name empty -> model output 0
 *   - outputs[0].scale <= 0 -> derive from output/input shape ratio, else the
 *     requested scale
 *   - outputs[0].crop > 0   -> raw output is scale * H + 2 * crop; trim crop
 *     pixels on every side
 *   - preprocess.norm       "0-1" (divide by 255), "0-255" (raw) or "-1-1"
 *     (divide by 127.5 and subtract 1)
 *   - preprocess.mean/std   optional 3-value arrays applied after norm:
 *     value = (value - mean[c]) / std[c] (for "0-255" the mean is in 0..255
 *     units, for the other modes in the normalized range)
 *   - constraints.fixedSize > 0 -> exact tile size
 *   - constraints.tileAlign > 0 -> round the requested tile size UP to a
 *     multiple of it
 *
 * TODO(P4): expose the sidecar JSON as an explicit chronos_session_create_ex()
 * parameter instead of deriving <model_dir>/model.json; the C ABI of
 * chronos_session_create() stays unchanged for now.
 */
#ifndef CHRONOS_ORT_SIDECAR_H
#define CHRONOS_ORT_SIDECAR_H

#include <string>
#include <vector>

namespace chronos {

struct OrtSidecarInput {
  std::string name;
  std::string source;
};

struct OrtSidecarOutput {
  std::string name;
  int scale = 0;
  int crop = 0;
};

struct OrtSidecarPreprocess {
  std::string norm = "0-1";
  int channels = 3;
  std::vector<float> mean;
  std::vector<float> std_values;
};

struct OrtSidecarConstraints {
  int tile_align = 1;
  int fixed_size = 0;
  int batch = 1;
  std::string dtype = "fp32";
};

struct OrtSidecar {
  bool present = false;
  std::string path;
  std::vector<OrtSidecarInput> inputs;
  std::vector<OrtSidecarOutput> outputs;
  OrtSidecarPreprocess preprocess;
  OrtSidecarConstraints constraints;
  /* Non-empty when the file exists but cannot be read or parsed. */
  std::string error;
};

/* <model_dir>/model.json (or "model.json" when the model path has no directory). */
std::string ort_sidecar_path_for_model(const std::string& model_path);

/* Missing file => present == false, defaults filled in, error empty. */
OrtSidecar load_ort_sidecar(const std::string& model_path);

}  // namespace chronos

#endif /* CHRONOS_ORT_SIDECAR_H */
