/*
 * Project Chronos - ONNX model introspection (see ort_inspect.h).
 *
 * Uses the dynamically loaded ONNX Runtime C API. The session is created only
 * to read the graph's input/output metadata and is released immediately, so the
 * cost is model-load time, not inference time.
 */
#include "ort_inspect.h"

#include <cstdint>
#include <cstdio>
#include <cstring>
#include <vector>

#include "ort_loader.h"
#include "platform_io.h"

namespace chronos {
namespace {

using OrtTensorElementDataType = ONNXTensorElementDataType;

struct TensorInfo {
  std::string name;
  std::vector<int64_t> dims;
  OrtTensorElementDataType type = ONNX_TENSOR_ELEMENT_DATA_TYPE_UNDEFINED;
};

std::string status_text(const OrtApi* api, OrtStatus* status) {
  if (status == nullptr) return std::string();
  std::string message = api->GetErrorMessage(status);
  api->ReleaseStatus(status);
  return message;
}

const char* element_type_name(OrtTensorElementDataType type) {
  switch (type) {
    case ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT:
      return "float32";
    case ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT16:
      return "float16";
    case ONNX_TENSOR_ELEMENT_DATA_TYPE_DOUBLE:
      return "float64";
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
    default:
      return "other";
  }
}

std::string json_escape(const std::string& value) {
  std::string escaped;
  escaped.reserve(value.size() + 8);
  for (char c : value) {
    switch (c) {
      case '"':
        escaped += "\\\"";
        break;
      case '\\':
        escaped += "\\\\";
        break;
      case '\n':
        escaped += "\\n";
        break;
      case '\r':
        escaped += "\\r";
        break;
      case '\t':
        escaped += "\\t";
        break;
      default:
        if (static_cast<unsigned char>(c) < 0x20) {
          escaped += '?';
        } else {
          escaped += c;
        }
        break;
    }
  }
  return escaped;
}

bool read_file(const std::string& path, std::vector<uint8_t>* data, std::string* error) {
  std::FILE* file = fopen_utf8(path, "rb");
  if (file == nullptr) {
    if (error != nullptr) *error = "cannot open model file: " + path;
    return false;
  }
  if (std::fseek(file, 0, SEEK_END) != 0) {
    std::fclose(file);
    if (error != nullptr) *error = "cannot seek model file: " + path;
    return false;
  }
  const long size = std::ftell(file);
  if (size < 0) {
    std::fclose(file);
    if (error != nullptr) *error = "cannot size model file: " + path;
    return false;
  }
  std::rewind(file);
  data->resize(static_cast<size_t>(size));
  const size_t read = size == 0 ? 0 : std::fread(data->data(), 1, data->size(), file);
  std::fclose(file);
  if (read != data->size()) {
    if (error != nullptr) *error = "short read on model file: " + path;
    return false;
  }
  return true;
}

bool query_tensors(const OrtApi* api, OrtSession* session, bool inputs,
                   std::vector<TensorInfo>* out, std::string* error) {
  size_t count = 0;
  OrtStatus* status =
      inputs ? api->SessionGetInputCount(session, &count) : api->SessionGetOutputCount(session, &count);
  if (status != nullptr) {
    if (error != nullptr) *error = "cannot query tensor count: " + status_text(api, status);
    return false;
  }
  OrtAllocator* allocator = nullptr;
  status = api->GetAllocatorWithDefaultOptions(&allocator);
  if (status != nullptr || allocator == nullptr) {
    if (error != nullptr) *error = "cannot get the ONNX Runtime allocator";
    return false;
  }
  for (size_t index = 0; index < count; ++index) {
    char* name = nullptr;
    status = inputs ? api->SessionGetInputName(session, index, allocator, &name)
                    : api->SessionGetOutputName(session, index, allocator, &name);
    if (status != nullptr) {
      if (error != nullptr) *error = "cannot query tensor name: " + status_text(api, status);
      return false;
    }
    OrtTypeInfo* type_info = nullptr;
    status = inputs ? api->SessionGetInputTypeInfo(session, index, &type_info)
                    : api->SessionGetOutputTypeInfo(session, index, &type_info);
    if (status != nullptr || type_info == nullptr) {
      if (error != nullptr) *error = "cannot query tensor type: " + status_text(api, status);
      return false;
    }
    const OrtTensorTypeAndShapeInfo* shape_info = nullptr;
    status = api->CastTypeInfoToTensorInfo(type_info, &shape_info);
    TensorInfo info;
    info.name = name != nullptr ? name : "";
    if (status != nullptr || shape_info == nullptr) {
      if (error != nullptr) {
        *error = "tensor is not a tensor type: " + info.name +
                 (status != nullptr ? std::string(" (") + status_text(api, status) + ")" : std::string());
      }
      api->ReleaseTypeInfo(type_info);
      return false;
    }
    size_t dim_count = 0;
    if (api->GetDimensionsCount(shape_info, &dim_count) == nullptr && dim_count > 0) {
      info.dims.resize(dim_count);
      if (api->GetDimensions(shape_info, info.dims.data(), dim_count) == nullptr) {
        // Ignore: keep unknown dims as -1 below.
      }
    }
    api->GetTensorElementType(shape_info, &info.type);
    api->ReleaseTypeInfo(type_info);
    out->push_back(info);
  }
  return true;
}

/* Integer scale from static spatial dims, 0 when it cannot be derived. */
int derive_scale(const std::vector<TensorInfo>& inputs, const std::vector<TensorInfo>& outputs) {
  if (inputs.size() != 1 || outputs.empty()) return 0;
  const TensorInfo& in = inputs.front();
  const TensorInfo& out = outputs.front();
  if (in.dims.size() != 4 || out.dims.size() != 4) return 0;
  const int64_t in_h = in.dims[2];
  const int64_t in_w = in.dims[3];
  const int64_t out_h = out.dims[2];
  const int64_t out_w = out.dims[3];
  if (in_h <= 0 || in_w <= 0 || out_h <= 0 || out_w <= 0) return 0;
  if (out_h % in_h != 0 || out_w % in_w != 0) return 0;
  const int64_t scale_h = out_h / in_h;
  const int64_t scale_w = out_w / in_w;
  if (scale_h != scale_w || scale_h < 1 || scale_h > 8) return 0;
  return static_cast<int>(scale_h);
}

std::string dims_json(const std::vector<int64_t>& dims) {
  std::string out = "[";
  for (size_t i = 0; i < dims.size(); ++i) {
    if (i != 0) out += ",";
    out += std::to_string(dims[i] > 0 ? dims[i] : -1);
  }
  out += "]";
  return out;
}

void append_tensor_json(std::string* json, const TensorInfo& info) {
  *json += "{\"name\":\"";
  *json += json_escape(info.name);
  *json += "\",\"dims\":";
  *json += dims_json(info.dims);
  *json += ",\"dtype\":\"";
  *json += element_type_name(info.type);
  *json += "\"}";
}

std::string escape_simple(const std::string& value) { return json_escape(value); }

}  // namespace

bool can_inspect_models() noexcept { return ort_api() != nullptr; }

bool inspect_model(const std::string& model_path, std::string* json, std::string* error) {
  if (json == nullptr) return false;
  json->clear();
  const OrtApi* api = ort_api();
  if (api == nullptr) {
    if (error != nullptr) {
      *error = "ONNX Runtime runtime not found (set CHRONOS_ORT_DIR or install it next to the app)";
    }
    return false;
  }

  std::vector<uint8_t> model_data;
  if (!read_file(model_path, &model_data, error)) return false;
  if (model_data.empty()) {
    if (error != nullptr) *error = "model file is empty: " + model_path;
    return false;
  }

  OrtEnv* env = nullptr;
  OrtSessionOptions* options = nullptr;
  OrtSession* session = nullptr;
  OrtStatus* status = api->CreateEnv(ORT_LOGGING_LEVEL_WARNING, "chronos-inspect", &env);
  if (status != nullptr) {
    if (error != nullptr) *error = "cannot create ONNX Runtime environment: " + status_text(api, status);
    return false;
  }
  status = api->CreateSessionOptions(&options);
  if (status == nullptr) status = api->SetIntraOpNumThreads(options, 1);
  if (status == nullptr) {
    status = api->CreateSessionFromArray(env, model_data.data(), model_data.size(), options, &session);
  }
  if (status != nullptr || session == nullptr) {
    const std::string message = status_text(api, status);
    if (error != nullptr) {
      *error = "cannot load ONNX model '" + model_path + "': " +
               (message.empty() ? std::string("unknown error") : message);
    }
    if (options != nullptr) api->ReleaseSessionOptions(options);
    if (env != nullptr) api->ReleaseEnv(env);
    return false;
  }

  std::vector<TensorInfo> inputs;
  std::vector<TensorInfo> outputs;
  std::string query_error;
  const bool ok = query_tensors(api, session, true, &inputs, &query_error) &&
                  query_tensors(api, session, false, &outputs, &query_error);
  api->ReleaseSession(session);
  api->ReleaseSessionOptions(options);
  api->ReleaseEnv(env);
  if (!ok) {
    if (error != nullptr) *error = query_error;
    return false;
  }

  const int scale = derive_scale(inputs, outputs);
  std::vector<std::string> warnings;
  if (inputs.size() != 1) {
    warnings.push_back("model has " + std::to_string(inputs.size()) +
                       " inputs: the SR contract supports exactly one image input");
  }
  if (!inputs.empty()) {
    const TensorInfo& in = inputs.front();
    if (in.dims.size() != 4) {
      warnings.push_back("input is not a 4D NCHW tensor");
    } else {
      if (in.dims[0] > 0 && in.dims[0] != 1) warnings.push_back("input batch is not 1");
      if (in.dims[1] > 0 && in.dims[1] != 3) warnings.push_back("input channel count is not 3");
      if (in.dims[2] <= 0 || in.dims[3] <= 0) warnings.push_back("input height/width are dynamic dims");
    }
    if (in.type != ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT &&
        in.type != ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT16) {
      warnings.push_back(std::string("input dtype is ") + element_type_name(in.type) +
                         ": the SR contract only guarantees float32/float16");
    }
  }
  if (outputs.empty()) {
    warnings.push_back("model has no outputs");
  } else {
    const TensorInfo& out = outputs.front();
    if (out.dims.size() != 4) {
      warnings.push_back("first output is not a 4D NCHW tensor");
    } else if (out.dims[1] > 0 && out.dims[1] != 3) {
      warnings.push_back("output channel count is not 3");
    }
    if (outputs.size() > 1) {
      warnings.push_back("model has " + std::to_string(outputs.size()) +
                         " outputs: the first 4D output is used (a sidecar can override it)");
    }
  }
  if (scale == 0) {
    warnings.push_back("cannot derive the scale from static shapes: set sidecar outputs[0].scale when importing");
  }

  std::string result = "{\"available\":true,\"scale\":";
  result += std::to_string(scale);
  result += ",\"inputs\":[";
  for (size_t i = 0; i < inputs.size(); ++i) {
    if (i != 0) result += ",";
    append_tensor_json(&result, inputs[i]);
  }
  result += "],\"outputs\":[";
  for (size_t i = 0; i < outputs.size(); ++i) {
    if (i != 0) result += ",";
    append_tensor_json(&result, outputs[i]);
  }
  result += "],\"warnings\":[";
  for (size_t i = 0; i < warnings.size(); ++i) {
    if (i != 0) result += ",";
    result += "\"";
    result += escape_simple(warnings[i]);
    result += "\"";
  }
  result += "],\"error\":\"\"}";
  *json = std::move(result);
  return true;
}

}  // namespace chronos
