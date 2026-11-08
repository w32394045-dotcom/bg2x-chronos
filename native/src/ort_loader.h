/*
 * Project Chronos - ONNX Runtime dynamic loader.
 *
 * ONNX Runtime is a *runtime* dependency: the application may download and ship
 * it later, and static import libraries are problematic for MinGW/cgo builds, so
 * nothing here is linked at build time. The loader opens onnxruntime.dll /
 * libonnxruntime.so on first use, resolves OrtGetApiBase and caches the OrtApi
 * function table. All entry points are safe to call when the library is absent.
 *
 * Search order for the library:
 *   1. CHRONOS_ORT_DIR environment variable (library directory, or the library
 *      file itself; `<dir>/lib/` from the official release zip is also tried)
 *   2. <executable dir>/ort/
 *   3. <executable dir>/runtime/ort/
 *   4. plain system load (onnxruntime.dll / libonnxruntime.so)
 *
 * The library is never unloaded: OrtApi function pointers and every OrtSession
 * stay valid for the process lifetime. A failed probe is retried on the next
 * call, so an application that downloads the runtime after startup can create a
 * session without restarting.
 */
#ifndef CHRONOS_ORT_LOADER_H
#define CHRONOS_ORT_LOADER_H

#include <string>

#include "onnxruntime/onnxruntime_c_api.h"

namespace chronos {

/* True when the library is loaded and a compatible OrtApi was obtained. */
bool ort_available() noexcept;

/* UTF-8 path of the loaded library ("" when not loaded). */
std::string ort_library_path();

/* ONNX Runtime version reported by the loaded library, e.g. "1.24.4" ("" when absent). */
std::string ort_version_string();

/*
 * Comma separated short names of the execution providers compiled into the
 * loaded runtime, CPU first, e.g. "CPU" or "CPU,DML". Empty when the library is
 * absent.
 */
std::string ort_providers();

/* OrtApi function table, or NULL when the library is not available. */
const OrtApi* ort_api() noexcept;

/* Opaque library handle for optional symbol lookups, or NULL. */
void* ort_library_handle() noexcept;

}  // namespace chronos

#endif /* CHRONOS_ORT_LOADER_H */
