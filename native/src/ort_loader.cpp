/*
 * Project Chronos - ONNX Runtime dynamic loader (implementation).
 *
 * See ort_loader.h for the search order and the lifetime rules. The loader
 * keeps a single process wide OrtApi; every failure path returns false / NULL
 * instead of throwing or aborting.
 */
#include "ort_loader.h"

#include <algorithm>
#include <mutex>
#include <string>
#include <vector>

#include "platform_io.h"

namespace chronos {
namespace {

#if defined(_WIN32)
const char* const kLibraryFileName = "onnxruntime.dll";
#else
const char* const kLibraryFileName = "libonnxruntime.so";
#endif

std::mutex g_mutex;
bool g_available = false;
void* g_handle = nullptr;
const OrtApi* g_api = nullptr;
std::string g_library_path;
std::string g_version;
std::string g_providers;

using GetApiBaseFunction = const OrtApiBase*(ORT_API_CALL*)(void);

std::string lowercase(std::string value) {
  for (char& c : value) {
    if (c >= 'A' && c <= 'Z') c = static_cast<char>(c - 'A' + 'a');
  }
  return value;
}

bool ends_with(const std::string& text, const std::string& suffix) {
  return text.size() >= suffix.size() &&
         text.compare(text.size() - suffix.size(), suffix.size(), suffix) == 0;
}

/* "CPUExecutionProvider" -> "CPU", "DmlExecutionProvider" -> "DML". Unknown
 * providers keep their upstream prefix without the generic suffix. */
std::string short_provider_name(const std::string& raw_name) {
  static const struct {
    const char* raw;
    const char* short_name;
  } kKnown[] = {
      {"CPUExecutionProvider", "CPU"},           {"DmlExecutionProvider", "DML"},
      {"CUDAExecutionProvider", "CUDA"},         {"TensorrtExecutionProvider", "TensorRT"},
      {"OpenVINOExecutionProvider", "OpenVINO"}, {"ROCMExecutionProvider", "ROCM"},
      {"MIGraphXExecutionProvider", "MIGraphX"}, {"CoreMLExecutionProvider", "CoreML"},
      {"XnnpackExecutionProvider", "XNNPACK"},   {"WebGpuExecutionProvider", "WebGPU"},
      {"WebNNExecutionProvider", "WebNN"},       {"VitisAIExecutionProvider", "VitisAI"},
      {"QNNExecutionProvider", "QNN"},           {"AzureExecutionProvider", "Azure"},
  };
  for (const auto& entry : kKnown) {
    if (raw_name == entry.raw) return entry.short_name;
  }
  const std::string suffix = "ExecutionProvider";
  if (ends_with(raw_name, suffix)) {
    return raw_name.substr(0, raw_name.size() - suffix.size());
  }
  return raw_name;
}

/* Comma separated short provider names, CPU first. Falls back to "CPU". */
std::string detect_providers(const OrtApi* api) {
  if (api == nullptr || api->GetAvailableProviders == nullptr) return "CPU";
  char** names = nullptr;
  int count = 0;
  OrtStatus* status = api->GetAvailableProviders(&names, &count);
  if (status != nullptr) {
    api->ReleaseStatus(status);
    return "CPU";
  }
  std::vector<std::string> detected;
  for (int i = 0; i < count; ++i) {
    if (names == nullptr || names[i] == nullptr) continue;
    const std::string name = short_provider_name(names[i]);
    if (!name.empty()) detected.push_back(name);
  }
  if (names != nullptr) {
    OrtStatus* release_status = api->ReleaseAvailableProviders(names, count);
    if (release_status != nullptr) api->ReleaseStatus(release_status);
  }
  std::sort(detected.begin(), detected.end());
  detected.erase(std::unique(detected.begin(), detected.end()), detected.end());
  if (detected.empty()) return "CPU";
  std::string result = "CPU";
  for (const std::string& name : detected) {
    if (name == "CPU") continue;
    result += ",";
    result += name;
  }
  return result;
}

std::vector<std::string> library_candidates() {
  std::vector<std::string> candidates;
  auto add = [&candidates](const std::string& path) {
    if (path.empty()) return;
    if (std::find(candidates.begin(), candidates.end(), path) != candidates.end()) return;
    candidates.push_back(path);
  };

  const std::string environment_dir = getenv_utf8("CHRONOS_ORT_DIR");
  if (!environment_dir.empty()) {
    const std::string lower = lowercase(environment_dir);
    if (ends_with(lower, ".dll") || ends_with(lower, ".so") || ends_with(lower, ".dylib")) {
      add(environment_dir);
    } else {
      add(join_path(environment_dir, kLibraryFileName));
      /* Accept the root of the official release zip as well as its lib/ dir. */
      add(join_path(join_path(environment_dir, "lib"), kLibraryFileName));
    }
  }

  const std::string executable_dir = executable_directory();
  if (!executable_dir.empty()) {
    add(join_path(join_path(executable_dir, "ort"), kLibraryFileName));
    add(join_path(join_path(join_path(executable_dir, "runtime"), "ort"), kLibraryFileName));
  }

  /* Plain system load: PATH on Windows, the loader search path on POSIX. */
  add(std::string(kLibraryFileName));
  return candidates;
}

bool ensure_loaded_locked() {
  if (g_available) return true;
  const std::vector<std::string> candidates = library_candidates();
  for (const std::string& candidate : candidates) {
    void* handle = load_shared_library(candidate);
    if (handle == nullptr) continue;

    const GetApiBaseFunction get_api_base = reinterpret_cast<GetApiBaseFunction>(
        shared_library_symbol(handle, "OrtGetApiBase"));
    if (get_api_base == nullptr) {
      close_shared_library(handle);
      continue;
    }
    const OrtApiBase* base = get_api_base();
    if (base == nullptr || base->GetApi == nullptr) {
      close_shared_library(handle);
      continue;
    }
    /* A header/runtime mismatch reports NULL here instead of crashing later. */
    const OrtApi* api = base->GetApi(static_cast<uint32_t>(ORT_API_VERSION));
    if (api == nullptr) {
      close_shared_library(handle);
      continue;
    }

    g_handle = handle;
    g_api = api;
    g_library_path = candidate;
    const char* version = base->GetVersionString != nullptr ? base->GetVersionString() : nullptr;
    g_version = version != nullptr ? version : "";
    g_providers = detect_providers(api);
    g_available = true;
    return true;
  }
  /* Probe failed: do not cache the failure, a later download + retry works. */
  return false;
}

}  // namespace

bool ort_available() noexcept {
  try {
    std::lock_guard<std::mutex> lock(g_mutex);
    return ensure_loaded_locked();
  } catch (...) {
    return false;
  }
}

std::string ort_library_path() {
  std::lock_guard<std::mutex> lock(g_mutex);
  if (!ensure_loaded_locked()) return std::string();
  return g_library_path;
}

std::string ort_version_string() {
  std::lock_guard<std::mutex> lock(g_mutex);
  if (!ensure_loaded_locked()) return std::string();
  return g_version;
}

std::string ort_providers() {
  std::lock_guard<std::mutex> lock(g_mutex);
  if (!ensure_loaded_locked()) return std::string();
  return g_providers;
}

const OrtApi* ort_api() noexcept {
  try {
    std::lock_guard<std::mutex> lock(g_mutex);
    return ensure_loaded_locked() ? g_api : nullptr;
  } catch (...) {
    return nullptr;
  }
}

void* ort_library_handle() noexcept {
  try {
    std::lock_guard<std::mutex> lock(g_mutex);
    return ensure_loaded_locked() ? g_handle : nullptr;
  } catch (...) {
    return nullptr;
  }
}

}  // namespace chronos
