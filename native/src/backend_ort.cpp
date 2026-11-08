/*
 * Project Chronos - ONNX Runtime backend (dynamic loading, no link-time dep).
 *
 * The backend talks to ONNX Runtime exclusively through the C API table obtained
 * from the dynamically loaded library (see ort_loader.h). Builds therefore never
 * need the ONNX Runtime import libraries, and the application can ship or
 * download the runtime later.
 *
 * Behaviour:
 *   - loads a .onnx model with OrtApi::CreateSessionFromArray (no path encoding
 *     issues, the model directory may contain non-ASCII characters),
 *   - reads the model input/output names and static shapes; an optional
 *     <model_dir>/model.json sidecar can override names, scale, crop, tile
 *     alignment, normalization and the input channel count,
 *   - runs one tile: uint8 RGB tile_size x tile_size -> float32 NCHW
 *     [1,3,H,W] (normalized per the sidecar) -> output [1,3,rawH,rawW] ->
 *     optional crop -> clamp to a uint8 RGB patch. Float outputs are treated as
 *     [0,1] when the tile maximum is <= 2 and as [0,255] otherwise (the same
 *     heuristic as the OpenVINO backend), so both common SR output ranges work,
 *   - native_scale() is the sidecar scale, else the output/input spatial ratio,
 *     else the requested scale,
 *   - describe() is "ort/<execution provider>" (e.g. "ort/CPU").
 *
 * Thread safety: at most one Run() is in flight per backend instance
 * (run_mutex_). The pipeline may call infer_tile() from several pool threads,
 * but the calls serialize here. That is intentional: the Go layer creates one
 * engine (one backend) per worker, so parallelism comes from running several
 * engines side by side; a single engine never grows N copies of intermediate
 * tensors. ONNX Runtime itself does allow concurrent Run() on one session.
 *
 * Execution provider selection: the loader reports the providers compiled into
 * the loaded runtime (ort_providers()). DML/CUDA are selected via their classic
 * dynamic entry points when present, with a transparent fallback to the CPU
 * execution provider if the runtime rejects the session (for example a missing
 * DirectML.dll). The CPU build of ONNX Runtime only exposes CPU, so that is the
 * path covered by the local test suite.
 */
#include <chronos/backend.h>

#include <algorithm>
#include <atomic>
#include <cstdint>
#include <cstdio>
#include <cstring>
#include <exception>
#include <memory>
#include <mutex>
#include <stdexcept>
#include <string>
#include <vector>

#include "chronos/chronos.h"
#include "chronos/options.h"
#include "common.h"
#include "ort_loader.h"
#include "ort_sidecar.h"
#include "platform_io.h"

namespace chronos {
namespace {

/* Refuse absurd model files instead of attempting a huge allocation. */
constexpr size_t kMaxModelBytes = static_cast<size_t>(2) * 1024 * 1024 * 1024;
/* Accepted range for the derived scale factor. */
constexpr int kMaxScale = 16;

enum class NormMode { kZeroOne, kZeroTwoFiftyFive, kMinusOneOne };

std::string status_text(const OrtApi* api, OrtStatus* status) {
  if (status == nullptr) return std::string();
  const char* message = api->GetErrorMessage(status);
  const std::string text = message != nullptr ? std::string(message) : std::string("unknown error");
  api->ReleaseStatus(status);
  return text;
}

const char* element_type_name(ONNXTensorElementDataType type) {
  switch (type) {
    case ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT:
      return "float32";
    case ONNX_TENSOR_ELEMENT_DATA_TYPE_UINT8:
      return "uint8";
    case ONNX_TENSOR_ELEMENT_DATA_TYPE_INT8:
      return "int8";
    case ONNX_TENSOR_ELEMENT_DATA_TYPE_UINT16:
      return "uint16";
    case ONNX_TENSOR_ELEMENT_DATA_TYPE_INT16:
      return "int16";
    case ONNX_TENSOR_ELEMENT_DATA_TYPE_INT32:
      return "int32";
    case ONNX_TENSOR_ELEMENT_DATA_TYPE_INT64:
      return "int64";
    case ONNX_TENSOR_ELEMENT_DATA_TYPE_STRING:
      return "string";
    case ONNX_TENSOR_ELEMENT_DATA_TYPE_BOOL:
      return "bool";
    case ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT16:
      return "float16";
    case ONNX_TENSOR_ELEMENT_DATA_TYPE_DOUBLE:
      return "float64";
    case ONNX_TENSOR_ELEMENT_DATA_TYPE_UINT32:
      return "uint32";
    case ONNX_TENSOR_ELEMENT_DATA_TYPE_UINT64:
      return "uint64";
    case ONNX_TENSOR_ELEMENT_DATA_TYPE_BFLOAT16:
      return "bfloat16";
    default:
      return "unknown";
  }
}

bool read_model_file(const std::string& path, std::vector<uint8_t>* data, std::string* err) {
  std::FILE* file = fopen_utf8(path, "rb");
  if (file == nullptr) {
    set_error(err, "cannot open ONNX model file '" + path + "'");
    return false;
  }
  data->clear();
  const size_t chunk_size = 1u << 20;
  std::vector<uint8_t> chunk(chunk_size);
  for (;;) {
    const size_t read = std::fread(chunk.data(), 1, chunk.size(), file);
    if (read > 0) {
      if (data->size() + read > kMaxModelBytes) {
        std::fclose(file);
        data->clear();
        set_error(err, "ONNX model file is larger than the 2 GiB limit");
        return false;
      }
      data->insert(data->end(), chunk.begin(), chunk.begin() + static_cast<std::ptrdiff_t>(read));
    }
    if (read < chunk.size()) {
      if (std::ferror(file) != 0) {
        std::fclose(file);
        data->clear();
        set_error(err, "cannot read ONNX model file '" + path + "'");
        return false;
      }
      break;
    }
  }
  std::fclose(file);
  if (data->empty()) {
    set_error(err, "ONNX model file is empty: '" + path + "'");
    return false;
  }
  return true;
}

uint8_t clamp_to_u8(float value) {
  /* The "!(value > 0)" form also converts NaN to 0 without a float-to-int
   * conversion of an out-of-range value (undefined behaviour). */
  if (!(value > 0.0f)) return 0;
  if (value >= 255.0f) return 255;
  return static_cast<uint8_t>(value + 0.5f);
}

struct TensorInfo {
  std::string name;
  ONNXTensorElementDataType element_type = ONNX_TENSOR_ELEMENT_DATA_TYPE_UNDEFINED;
  std::vector<int64_t> dims;
};

std::string join_names(const std::vector<std::string>& names) {
  std::string result;
  for (const std::string& name : names) {
    if (!result.empty()) result += ", ";
    result += name;
  }
  return result;
}

class OrtBackend final : public IBackend {
 public:
  OrtBackend(const std::string& model_path, int requested_scale, const Options& options) {
    try {
      initialize(model_path, requested_scale, options);
    } catch (...) {
      release_resources();
      throw;
    }
  }

  ~OrtBackend() override { release_resources(); }

  int id() const noexcept override { return CHRONOS_BACKEND_ORT; }

  int native_scale() const noexcept override { return scale_; }

  std::string describe() const override { return "ort/" + execution_provider_; }

  bool supports_batch() const noexcept override { return false; }

  int tile_size_for(int requested_tile_size) const noexcept override {
    /* A model with a static input size, or a sidecar constraints.fixedSize,
     * dictates the tile size; otherwise the caller's tile is rounded UP to
     * constraints.tileAlign. */
    if (tile_size_ > 0) return tile_size_;
    int value = requested_tile_size > 0 ? requested_tile_size : kDefaultTileSize;
    if (align_ > 1) {
      const int remainder = value % align_;
      if (remainder != 0) value += align_ - remainder;
    }
    return value;
  }

  bool infer_tile(const uint8_t* tile, int tile_size, uint8_t* patch, int patch_size,
                  TileWorkspace& workspace, std::string* err) override {
    (void)workspace;
    if (tile == nullptr || patch == nullptr) {
      set_error(err, "ONNX Runtime backend: null tile or patch buffer");
      return false;
    }
    std::lock_guard<std::mutex> lock(run_mutex_);
    try {
      return run_tile(tile, tile_size, patch, patch_size, err);
    } catch (const std::exception& exception) {
      set_error(err, std::string("ONNX Runtime backend: ") + exception.what());
      return false;
    } catch (...) {
      set_error(err, "ONNX Runtime backend: unknown error");
      return false;
    }
  }

 private:
  void initialize(const std::string& model_path, int requested_scale, const Options& options) {
    model_path_ = model_path;
    api_ = ort_api();
    if (api_ == nullptr) {
      throw std::runtime_error("ONNX Runtime runtime not found (set CHRONOS_ORT_DIR)");
    }

    const OrtSidecar sidecar = load_ort_sidecar(model_path);
    if (!sidecar.error.empty()) throw std::runtime_error(sidecar.error);
    apply_sidecar(sidecar);

    std::vector<uint8_t> model_data;
    std::string error;
    if (!read_model_file(model_path, &model_data, &error)) throw std::runtime_error(error);

    OrtStatus* status = api_->CreateEnv(ORT_LOGGING_LEVEL_WARNING, "chronos", &env_);
    if (status != nullptr) {
      throw std::runtime_error("cannot create ONNX Runtime environment: " + status_text(api_, status));
    }

    /* Try the best available execution provider first, then the CPU EP. The
     * CPU EP needs no options, so any failure leaves a working configuration. */
    const std::string preferred = preferred_execution_provider();
    std::vector<std::string> candidates;
    candidates.push_back(preferred);
    if (preferred != "CPU") candidates.push_back("CPU");

    std::string last_error;
    for (const std::string& provider : candidates) {
      if (session_options_ != nullptr) {
        api_->ReleaseSessionOptions(session_options_);
        session_options_ = nullptr;
      }
      create_session_options(options);
      if (provider != "CPU") {
        std::string append_error;
        if (!append_device_provider(provider, options.device_index, &append_error)) {
          last_error = append_error;
          continue;
        }
      }
      session_ = nullptr;
      OrtStatus* create_status = api_->CreateSessionFromArray(env_, model_data.data(),
                                                              model_data.size(),
                                                              session_options_, &session_);
      if (create_status == nullptr) {
        execution_provider_ = provider;
        break;
      }
      last_error = status_text(api_, create_status);
    }
    if (session_ == nullptr) {
      throw std::runtime_error("cannot load ONNX model '" + model_path + "': " +
                               (last_error.empty() ? std::string("unknown error") : last_error));
    }

    status = api_->GetAllocatorWithDefaultOptions(&allocator_);
    if (status != nullptr || allocator_ == nullptr) {
      throw std::runtime_error("cannot get the ONNX Runtime default allocator: " +
                               status_text(api_, status));
    }

    session_input_count_ = 0;
    session_output_count_ = 0;
    if (!query_tensor_count(true, &session_input_count_, &error)) throw std::runtime_error(error);
    if (!query_tensor_count(false, &session_output_count_, &error)) throw std::runtime_error(error);
    if (session_input_count_ == 0) throw std::runtime_error("ONNX model has no inputs");
    if (session_output_count_ == 0) throw std::runtime_error("ONNX model has no outputs");

    const size_t input_index = resolve_tensor_index(true, sidecar, &error);
    if (input_index == session_input_count_) throw std::runtime_error(error);
    const size_t output_index = resolve_tensor_index(false, sidecar, &error);
    if (output_index == session_output_count_) throw std::runtime_error(error);

    TensorInfo input;
    TensorInfo output;
    if (!query_tensor_info(true, input_index, &input, &error)) throw std::runtime_error(error);
    if (!query_tensor_info(false, output_index, &output, &error)) throw std::runtime_error(error);
    input_name_ = input.name;
    output_name_ = output.name;

    configure_from_shapes(input, output, requested_scale);

    status = api_->CreateCpuMemoryInfo(OrtArenaAllocator, OrtMemTypeDefault, &memory_info_);
    if (status != nullptr || memory_info_ == nullptr) {
      throw std::runtime_error("cannot create ONNX Runtime CPU memory info: " +
                               status_text(api_, status));
    }
  }

  /* Applies the defaults and validates the preprocess settings of the sidecar. */
  void apply_sidecar(const OrtSidecar& sidecar) {
    if (sidecar.preprocess.channels != 3) {
      throw std::runtime_error("model.json: only 3-channel RGB input is supported (preprocess.channels=" +
                               std::to_string(sidecar.preprocess.channels) + ")");
    }
    if (sidecar.preprocess.norm == "0-1") {
      norm_ = NormMode::kZeroOne;
    } else if (sidecar.preprocess.norm == "0-255") {
      norm_ = NormMode::kZeroTwoFiftyFive;
    } else if (sidecar.preprocess.norm == "-1-1") {
      norm_ = NormMode::kMinusOneOne;
    } else {
      throw std::runtime_error("model.json: unsupported preprocess.norm '" + sidecar.preprocess.norm +
                               "' (supported: 0-1, 0-255, -1-1)");
    }
    mean_ = sidecar.preprocess.mean;
    std_values_ = sidecar.preprocess.std_values;
    crop_ = sidecar.outputs.empty() ? 0 : sidecar.outputs[0].crop;
    align_ = sidecar.constraints.tile_align > 0 ? sidecar.constraints.tile_align : 1;
    sidecar_fixed_size_ = sidecar.constraints.fixed_size;
    sidecar_scale_ = sidecar.outputs.empty() ? 0 : sidecar.outputs[0].scale;
  }

  void create_session_options(const Options& options) {
    OrtStatus* status = api_->CreateSessionOptions(&session_options_);
    if (status != nullptr) {
      throw std::runtime_error("cannot create ONNX Runtime session options: " +
                               status_text(api_, status));
    }
    status = api_->SetSessionGraphOptimizationLevel(session_options_, ORT_ENABLE_ALL);
    if (status != nullptr) {
      throw std::runtime_error("cannot set ONNX Runtime graph optimization level: " +
                               status_text(api_, status));
    }
    int threads = options.effective_threads();
    if (threads < 1) threads = 1;
    status = api_->SetIntraOpNumThreads(session_options_, threads);
    if (status != nullptr) {
      throw std::runtime_error("cannot set ONNX Runtime intra-op threads: " +
                               status_text(api_, status));
    }
    /* One inter-op thread: tiles are already parallelised across engines. */
    status = api_->SetInterOpNumThreads(session_options_, 1);
    if (status != nullptr) {
      throw std::runtime_error("cannot set ONNX Runtime inter-op threads: " +
                               status_text(api_, status));
    }
  }

  std::string preferred_execution_provider() {
    const std::string providers = ort_providers();
    if (provider_listed(providers, "DML") &&
        shared_library_symbol(ort_library_handle(),
                              "OrtSessionOptionsAppendExecutionProvider_DML") != nullptr) {
      return "DML";
    }
    if (provider_listed(providers, "CUDA") &&
        shared_library_symbol(ort_library_handle(),
                              "OrtSessionOptionsAppendExecutionProvider_CUDA") != nullptr) {
      return "CUDA";
    }
    return "CPU";
  }

  static bool provider_listed(const std::string& providers, const std::string& name) {
    size_t start = 0;
    while (start <= providers.size()) {
      const size_t comma = providers.find(',', start);
      const std::string token = providers.substr(
          start, comma == std::string::npos ? std::string::npos : comma - start);
      if (token == name) return true;
      if (comma == std::string::npos) break;
      start = comma + 1;
    }
    return false;
  }

  /* The classic dynamic entry points: OrtStatus*(OrtSessionOptions*, int device_id). */
  bool append_device_provider(const std::string& provider, int device_index, std::string* err) {
    const char* symbol_name = nullptr;
    if (provider == "DML") {
      symbol_name = "OrtSessionOptionsAppendExecutionProvider_DML";
    } else if (provider == "CUDA") {
      symbol_name = "OrtSessionOptionsAppendExecutionProvider_CUDA";
    } else {
      set_error(err, "unsupported ONNX Runtime execution provider '" + provider + "'");
      return false;
    }
    void* symbol = shared_library_symbol(ort_library_handle(), symbol_name);
    if (symbol == nullptr) {
      set_error(err, std::string("execution provider ") + provider +
                         " is not exported by the loaded ONNX Runtime library");
      return false;
    }
    using AppendFunction = OrtStatus*(ORT_API_CALL*)(OrtSessionOptions*, int);
    const AppendFunction append = reinterpret_cast<AppendFunction>(symbol);
    OrtStatus* status = append(session_options_, device_index);
    if (status != nullptr) {
      set_error(err, provider + " execution provider setup failed: " + status_text(api_, status));
      return false;
    }
    return true;
  }

  bool query_tensor_count(bool input, size_t* count, std::string* err) {
    OrtStatus* status = input ? api_->SessionGetInputCount(session_, count)
                              : api_->SessionGetOutputCount(session_, count);
    if (status != nullptr) {
      set_error(err, std::string("cannot query ONNX model ") + (input ? "inputs: " : "outputs: ") +
                         status_text(api_, status));
      return false;
    }
    return true;
  }

  bool query_tensor_name(bool input, size_t index, std::string* name, std::string* err) {
    char* raw_name = nullptr;
    OrtStatus* status = input
                            ? api_->SessionGetInputName(session_, index, allocator_, &raw_name)
                            : api_->SessionGetOutputName(session_, index, allocator_, &raw_name);
    if (status != nullptr) {
      set_error(err, std::string("cannot query ONNX model ") +
                         (input ? "input name: " : "output name: ") + status_text(api_, status));
      return false;
    }
    *name = raw_name != nullptr ? std::string(raw_name) : std::string();
    if (raw_name != nullptr) allocator_->Free(allocator_, raw_name);
    return true;
  }

  bool query_tensor_info(bool input, size_t index, TensorInfo* info, std::string* err) {
    if (!query_tensor_name(input, index, &info->name, err)) return false;

    OrtTypeInfo* type_info = nullptr;
    OrtStatus* status =
        input ? api_->SessionGetInputTypeInfo(session_, index, &type_info)
              : api_->SessionGetOutputTypeInfo(session_, index, &type_info);
    if (status != nullptr) {
      set_error(err, std::string("cannot query ONNX model ") +
                         (input ? "input type: " : "output type: ") + status_text(api_, status));
      return false;
    }
    if (type_info == nullptr) {
      set_error(err, "ONNX model returned no type information");
      return false;
    }
    const OrtTensorTypeAndShapeInfo* tensor_info = nullptr;
    status = api_->CastTypeInfoToTensorInfo(type_info, &tensor_info);
    if (status != nullptr || tensor_info == nullptr) {
      const std::string message = status_text(api_, status);
      api_->ReleaseTypeInfo(type_info);
      set_error(err, std::string("ONNX model ") + (input ? "input" : "output") +
                         " is not a tensor" + (message.empty() ? "" : (": " + message)));
      return false;
    }
    status = api_->GetTensorElementType(tensor_info, &info->element_type);
    if (status == nullptr) {
      size_t rank = 0;
      status = api_->GetDimensionsCount(tensor_info, &rank);
      if (status == nullptr && rank > 8) {
        api_->ReleaseTypeInfo(type_info);
        set_error(err, "ONNX model tensor rank is larger than the supported maximum (8)");
        return false;
      }
      if (status == nullptr) {
        info->dims.assign(rank, 0);
        status = api_->GetDimensions(tensor_info, info->dims.data(), rank);
      }
    }
    api_->ReleaseTypeInfo(type_info);
    if (status != nullptr) {
      set_error(err, std::string("cannot read ONNX model ") + (input ? "input" : "output") +
                         " shape: " + status_text(api_, status));
      return false;
    }
    return true;
  }

  /* Returns the tensor index, or `count` when the sidecar name was not found
   * (the caller detects that sentinel and reports *err). */
  size_t resolve_tensor_index(bool input, const OrtSidecar& sidecar, std::string* err) {
    const size_t count = input ? session_input_count_ : session_output_count_;
    std::string requested;
    if (input) {
      if (!sidecar.inputs.empty()) requested = sidecar.inputs[0].name;
    } else {
      if (!sidecar.outputs.empty()) requested = sidecar.outputs[0].name;
    }
    if (requested.empty()) return 0;

    std::vector<std::string> available;
    for (size_t index = 0; index < count; ++index) {
      std::string name;
      if (!query_tensor_name(input, index, &name, err)) return count;
      if (name == requested) return index;
      available.push_back(name);
    }
    set_error(err, std::string("model.json: ") + (input ? "input" : "output") + " '" + requested +
                       "' not found in the model (model " + (input ? "inputs" : "outputs") +
                       ": " + join_names(available) + ")");
    return count;
  }

  void configure_from_shapes(const TensorInfo& input, const TensorInfo& output,
                             int requested_scale) {
    if (input.dims.size() != 4) {
      throw std::runtime_error("unsupported ONNX model input rank " +
                               std::to_string(input.dims.size()) +
                               " (expected 4: [1,3,H,W])");
    }
    if (input.element_type != ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT) {
      throw std::runtime_error(std::string("unsupported ONNX model input type ") +
                               element_type_name(input.element_type) +
                               " (expected float32)");
    }
    if (input.dims[0] > 0 && input.dims[0] != 1) {
      throw std::runtime_error("unsupported ONNX model input batch size " +
                               std::to_string(input.dims[0]) + " (expected 1)");
    }
    if (input.dims[1] > 0 && input.dims[1] != 3) {
      throw std::runtime_error("unsupported ONNX model input channels " +
                               std::to_string(input.dims[1]) + " (expected 3)");
    }

    bool model_fixed_tile = false;
    int model_tile_size = 0;
    if (input.dims[2] > 0 && input.dims[3] > 0) {
      if (input.dims[2] != input.dims[3]) {
        throw std::runtime_error("unsupported ONNX model input shape: H and W must be equal");
      }
      model_fixed_tile = true;
      model_tile_size = static_cast<int>(input.dims[2]);
      if (model_tile_size < kMinTileSize || model_tile_size > kMaxTileSize) {
        throw std::runtime_error("ONNX model input tile size " + std::to_string(model_tile_size) +
                                 " is outside the supported range " +
                                 std::to_string(kMinTileSize) + ".." + std::to_string(kMaxTileSize));
      }
    }

    if (output.dims.size() != 4) {
      throw std::runtime_error("unsupported ONNX model output rank " +
                               std::to_string(output.dims.size()) +
                               " (expected 4: [1,3,H,W])");
    }
    if (output.element_type != ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT) {
      throw std::runtime_error(std::string("unsupported ONNX model output type ") +
                               element_type_name(output.element_type) +
                               " (expected float32)");
    }
    if (output.dims[0] > 0 && output.dims[0] != 1) {
      throw std::runtime_error("unsupported ONNX model output batch size " +
                               std::to_string(output.dims[0]) + " (expected 1)");
    }
    if (output.dims[1] > 0 && output.dims[1] != 3) {
      throw std::runtime_error("unsupported ONNX model output channels " +
                               std::to_string(output.dims[1]) + " (expected 3)");
    }

    /* Sidecar fixedSize has to agree with a static model input. */
    if (sidecar_fixed_size_ > 0) {
      if (sidecar_fixed_size_ < kMinTileSize || sidecar_fixed_size_ > kMaxTileSize) {
        throw std::runtime_error("model.json: constraints.fixedSize " +
                                 std::to_string(sidecar_fixed_size_) +
                                 " is outside the supported range " +
                                 std::to_string(kMinTileSize) + ".." +
                                 std::to_string(kMaxTileSize));
      }
      if (model_fixed_tile && sidecar_fixed_size_ != model_tile_size) {
        throw std::runtime_error("model.json: constraints.fixedSize " +
                                 std::to_string(sidecar_fixed_size_) +
                                 " does not match the model input size " +
                                 std::to_string(model_tile_size));
      }
      if (!model_fixed_tile) tile_size_ = sidecar_fixed_size_;
    } else if (model_fixed_tile) {
      tile_size_ = model_tile_size;
    }

    int derived_scale = 0;
    const bool output_static = output.dims[2] > 0 && output.dims[3] > 0;
    if (output_static) {
      if (output.dims[2] != output.dims[3]) {
        throw std::runtime_error("unsupported ONNX model output shape: H and W must be equal");
      }
      if (model_fixed_tile) {
        const int64_t height_span = output.dims[2] - 2LL * static_cast<int64_t>(crop_);
        const int64_t width_span = output.dims[3] - 2LL * static_cast<int64_t>(crop_);
        if (height_span <= 0 || width_span <= 0) {
          throw std::runtime_error("ONNX model output is too small for the configured crop");
        }
        if (height_span % model_tile_size != 0 || width_span % model_tile_size != 0) {
          throw std::runtime_error(
              "ONNX model output size is not an integer multiple of the input size");
        }
        const int64_t scale_height = height_span / model_tile_size;
        const int64_t scale_width = width_span / model_tile_size;
        if (scale_height != scale_width) {
          throw std::runtime_error("ONNX model output aspect ratio does not match the input");
        }
        if (scale_height < 1 || scale_height > kMaxScale) {
          throw std::runtime_error("ONNX model scale " + std::to_string(scale_height) +
                                   " is outside the supported range 1.." +
                                   std::to_string(kMaxScale));
        }
        derived_scale = static_cast<int>(scale_height);
      }
    }

    if (sidecar_scale_ > 0) {
      scale_ = sidecar_scale_;
      if (derived_scale > 0 && derived_scale != scale_) {
        throw std::runtime_error("model.json: outputs[0].scale " + std::to_string(scale_) +
                                 " does not match the model output/input ratio " +
                                 std::to_string(derived_scale));
      }
    } else if (derived_scale > 0) {
      scale_ = derived_scale;
    } else if (requested_scale > 0) {
      scale_ = requested_scale;
    } else {
      throw std::runtime_error(
          "cannot determine the model scale: set outputs[0].scale in model.json next to the "
          "model, or pass an explicit scale");
    }
    if (scale_ < 1 || scale_ > kMaxScale) {
      throw std::runtime_error("model scale " + std::to_string(scale_) +
                               " is outside the supported range 1.." + std::to_string(kMaxScale));
    }
    if (requested_scale > 0 && requested_scale != scale_) {
      throw std::runtime_error("requested scale " + std::to_string(requested_scale) +
                               " does not match model scale " + std::to_string(scale_));
    }

    /* Early validation when both shapes are static: catches a wrong crop or a
     * mismatched sidecar scale before the first tile is inferred. */
    if (output_static && tile_size_ > 0) {
      const int64_t expected_raw = static_cast<int64_t>(scale_) * tile_size_ +
                                   2LL * static_cast<int64_t>(crop_);
      if (output.dims[2] != expected_raw) {
        throw std::runtime_error(
            "ONNX model output size " + std::to_string(output.dims[2]) +
            " does not match scale " + std::to_string(scale_) + " * tile " +
            std::to_string(tile_size_) + " + 2 * crop " + std::to_string(crop_));
      }
    }
  }

  int resolve_tile_size(int tile_size) const {
    if (tile_size_ > 0) return tile_size_;
    if (align_ > 1 && tile_size % align_ != 0) return -1;
    return tile_size;
  }

  bool run_tile(const uint8_t* tile_rgb, int tile_size, uint8_t* patch, int patch_size,
                std::string* err) {
    const int expected_tile = resolve_tile_size(tile_size);
    if (expected_tile < 0) {
      set_error(err, "ONNX Runtime backend: tile size " + std::to_string(tile_size) +
                         " is not a multiple of constraints.tileAlign " + std::to_string(align_));
      return false;
    }
    if (expected_tile != tile_size) {
      set_error(err, "ONNX Runtime backend: expected a tile of " +
                         std::to_string(expected_tile) + " x " + std::to_string(expected_tile) +
                         ", got " + std::to_string(tile_size));
      return false;
    }
    const int expected_patch = tile_size * scale_;
    if (patch_size != expected_patch) {
      set_error(err, "ONNX Runtime backend: expected a patch of " +
                         std::to_string(expected_patch) + " x " + std::to_string(expected_patch) +
                         ", got " + std::to_string(patch_size));
      return false;
    }

    /* NCHW float input; the buffer is reused across tiles (guarded by run_mutex_). */
    const size_t plane = static_cast<size_t>(tile_size) * static_cast<size_t>(tile_size);
    const size_t element_count = plane * 3;
    if (input_buffer_.size() != element_count) input_buffer_.resize(element_count);
    for (int channel = 0; channel < 3; ++channel) {
      float* destination = input_buffer_.data() + plane * static_cast<size_t>(channel);
      for (size_t i = 0; i < plane; ++i) {
        destination[i] = normalize_pixel(tile_rgb[i * 3 + static_cast<size_t>(channel)], channel);
      }
    }

    const int64_t shape[4] = {1, 3, tile_size, tile_size};
    OrtValue* input_value = nullptr;
    OrtStatus* status = api_->CreateTensorWithDataAsOrtValue(
        memory_info_, input_buffer_.data(), element_count * sizeof(float), shape, 4,
        ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT, &input_value);
    if (status != nullptr || input_value == nullptr) {
      set_error(err, "cannot create ONNX Runtime input tensor: " + status_text(api_, status));
      if (input_value != nullptr) api_->ReleaseValue(input_value);
      return false;
    }

    const char* input_names[1] = {input_name_.c_str()};
    const OrtValue* inputs[1] = {input_value};
    const char* output_names[1] = {output_name_.c_str()};
    OrtValue* outputs[1] = {nullptr};
    status = api_->Run(session_, nullptr, input_names, inputs, 1, output_names, 1, outputs);
    api_->ReleaseValue(input_value);
    if (status != nullptr) {
      set_error(err, "ONNX Runtime inference failed for '" + model_path_ +
                         "': " + status_text(api_, status));
      return false;
    }
    if (outputs[0] == nullptr) {
      set_error(err, "ONNX Runtime inference returned no output tensor");
      return false;
    }
    const bool copied = copy_output_to_patch(outputs[0], tile_size, patch, err);
    api_->ReleaseValue(outputs[0]);
    return copied;
  }

  bool copy_output_to_patch(OrtValue* output_value, int tile_size, uint8_t* patch,
                            std::string* err) {
    OrtTensorTypeAndShapeInfo* tensor_info = nullptr;
    OrtStatus* status = api_->GetTensorTypeAndShape(output_value, &tensor_info);
    if (status != nullptr || tensor_info == nullptr) {
      set_error(err, "cannot read ONNX Runtime output shape: " + status_text(api_, status));
      return false;
    }

    ONNXTensorElementDataType element_type = ONNX_TENSOR_ELEMENT_DATA_TYPE_UNDEFINED;
    size_t rank = 0;
    int64_t dims[4] = {0, 0, 0, 0};
    status = api_->GetTensorElementType(tensor_info, &element_type);
    if (status == nullptr) status = api_->GetDimensionsCount(tensor_info, &rank);
    if (status == nullptr && rank == 4) status = api_->GetDimensions(tensor_info, dims, 4);
    api_->ReleaseTensorTypeAndShapeInfo(tensor_info);
    if (status != nullptr) {
      set_error(err, "cannot read ONNX Runtime output shape: " + status_text(api_, status));
      return false;
    }
    if (rank != 4 || (dims[0] > 0 && dims[0] != 1) || (dims[1] > 0 && dims[1] != 3)) {
      set_error(err, "ONNX Runtime output must be [1,3,H,W]");
      return false;
    }
    if (element_type != ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT) {
      set_error(err, std::string("ONNX Runtime backend: unsupported output type ") +
                         element_type_name(element_type) + " (expected float32)");
      return false;
    }
    const int raw_height = static_cast<int>(dims[2]);
    const int raw_width = static_cast<int>(dims[3]);
    const int patch_size = tile_size * scale_;
    const int expected_raw = patch_size + 2 * crop_;
    if (raw_height != expected_raw || raw_width != expected_raw) {
      set_error(err, "ONNX Runtime model returned " + std::to_string(raw_height) + "x" +
                         std::to_string(raw_width) + " but " + std::to_string(expected_raw) +
                         "x" + std::to_string(expected_raw) + " was expected (scale " +
                         std::to_string(scale_) + ", tile " + std::to_string(tile_size) +
                         ", crop " + std::to_string(crop_) +
                         "); check outputs[0].scale/crop in model.json");
      return false;
    }

    void* raw_data = nullptr;
    status = api_->GetTensorMutableData(output_value, &raw_data);
    if (status != nullptr || raw_data == nullptr) {
      set_error(err, "cannot access ONNX Runtime output data: " + status_text(api_, status));
      return false;
    }
    const float* data = static_cast<const float*>(raw_data);
    const size_t raw_plane = static_cast<size_t>(raw_height) * static_cast<size_t>(raw_width);

    /* SR models usually emit [0,1]; some emit [0,255]. Detect per tile, exactly
     * like the OpenVINO backend does, unless the tile is empty. */
    double maximum = 0.0;
    for (int channel = 0; channel < 3; ++channel) {
      const float* channel_data = data + raw_plane * static_cast<size_t>(channel);
      for (int y = 0; y < patch_size; ++y) {
        const float* row = channel_data +
                           static_cast<size_t>(y + crop_) * static_cast<size_t>(raw_width) +
                           static_cast<size_t>(crop_);
        for (int x = 0; x < patch_size; ++x) {
          const double value = static_cast<double>(row[x]);
          if (value > maximum) maximum = value;
        }
      }
    }
    const float gain = maximum <= 2.0 ? 255.0f : 1.0f;
    for (int channel = 0; channel < 3; ++channel) {
      const float* channel_data = data + raw_plane * static_cast<size_t>(channel);
      for (int y = 0; y < patch_size; ++y) {
        const float* row = channel_data +
                           static_cast<size_t>(y + crop_) * static_cast<size_t>(raw_width) +
                           static_cast<size_t>(crop_);
        uint8_t* destination = patch +
                               (static_cast<size_t>(y) * static_cast<size_t>(patch_size) * 3) +
                               static_cast<size_t>(channel);
        for (int x = 0; x < patch_size; ++x) {
          destination[static_cast<size_t>(x) * 3] = clamp_to_u8(row[x] * gain);
        }
      }
    }
    return true;
  }

  float normalize_pixel(uint8_t value, int channel) const {
    float normalized = static_cast<float>(value);
    switch (norm_) {
      case NormMode::kZeroOne:
        normalized /= 255.0f;
        break;
      case NormMode::kMinusOneOne:
        normalized = normalized / 127.5f - 1.0f;
        break;
      case NormMode::kZeroTwoFiftyFive:
        break;
    }
    if (mean_.size() == 3) normalized -= mean_[static_cast<size_t>(channel)];
    if (std_values_.size() == 3) normalized /= std_values_[static_cast<size_t>(channel)];
    return normalized;
  }

  void release_resources() noexcept {
    if (api_ == nullptr) return;
    if (memory_info_ != nullptr) {
      api_->ReleaseMemoryInfo(memory_info_);
      memory_info_ = nullptr;
    }
    if (session_ != nullptr) {
      api_->ReleaseSession(session_);
      session_ = nullptr;
    }
    if (session_options_ != nullptr) {
      api_->ReleaseSessionOptions(session_options_);
      session_options_ = nullptr;
    }
    if (env_ != nullptr) {
      api_->ReleaseEnv(env_);
      env_ = nullptr;
    }
    allocator_ = nullptr;
  }

  const OrtApi* api_ = nullptr;
  OrtEnv* env_ = nullptr;
  OrtSessionOptions* session_options_ = nullptr;
  OrtSession* session_ = nullptr;
  OrtMemoryInfo* memory_info_ = nullptr;
  OrtAllocator* allocator_ = nullptr;

  std::string model_path_;
  std::string input_name_;
  std::string output_name_;
  std::string execution_provider_ = "CPU";

  size_t session_input_count_ = 0;
  size_t session_output_count_ = 0;

  NormMode norm_ = NormMode::kZeroOne;
  std::vector<float> mean_;
  std::vector<float> std_values_;
  int crop_ = 0;
  int align_ = 1;
  int sidecar_fixed_size_ = 0;
  int sidecar_scale_ = 0;

  int scale_ = 1;
  /* > 0 when the model input size (or constraints.fixedSize) is fixed. */
  int tile_size_ = 0;

  std::vector<float> input_buffer_;
  std::mutex run_mutex_;
};

}  // namespace

std::unique_ptr<IBackend> create_ort_backend(const std::string& model_path, int requested_scale,
                                             const Options& options, std::string* err) {
  if (model_path.empty()) {
    set_error(err, "ONNX Runtime backend requires a model path");
    return nullptr;
  }
  try {
    return std::unique_ptr<IBackend>(new OrtBackend(model_path, requested_scale, options));
  } catch (const std::exception& exception) {
    set_error(err, exception.what());
  } catch (...) {
    set_error(err, "cannot create ONNX Runtime backend: unknown error");
  }
  return nullptr;
}

}  // namespace chronos
