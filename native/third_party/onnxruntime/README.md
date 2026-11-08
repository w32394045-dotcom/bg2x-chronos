# ONNX Runtime C API header (vendored)

This directory contains the public C API header of ONNX Runtime, used only at
compile time. The runtime library itself is loaded dynamically at process
startup/session creation (see `native/src/ort_loader.{h,cpp}`), so linking the
Chronos native library never requires ONNX Runtime import libraries.

| Item | Value |
| --- | --- |
| Files | `onnxruntime_c_api.h`, `onnxruntime_ep_c_api.h` |
| Upstream version | ONNX Runtime 1.24.4 (tag `v1.24.4`) |
| Upstream path | `include/onnxruntime/core/session/` |
| Source URL | https://raw.githubusercontent.com/microsoft/onnxruntime/v1.24.4/include/onnxruntime/core/session/onnxruntime_c_api.h (and `onnxruntime_ep_c_api.h`) |
| License | MIT (Copyright (c) Microsoft Corporation) |
| `ORT_API_VERSION` in this header | 24 |

`onnxruntime_c_api.h` includes `onnxruntime_ep_c_api.h` since ONNX Runtime 1.24
(new execution-provider C API), so both files are vendored at the same tag; the
two headers include each other and are meant to be used together.

## Local modification

The upstream file is byte-for-byte identical except that two U+00A0
(non-breaking space) characters that upstream has in a Doxygen comment were
replaced with regular ASCII spaces. The repository requires ASCII-only text in
the native tree (historical encoding problems on Windows), and this is a
comment-only change. No declarations, macros or values are modified.

## Updating

1. Download the header from the new tag into this directory (same name).
2. Replace non-ASCII bytes with ASCII equivalents (verify with a byte scan).
3. Bump the version in this file and update the `ORT_API_VERSION` expectation
   if the new header changes it.
4. Run `ctest -R native_ort` with the matching runtime; the loader requests
   `ORT_API_VERSION` from the loaded library, so header/runtime major mismatches
   surface as "incompatible ONNX Runtime" instead of a crash.

## Runtime discovery

The native layer loads `onnxruntime.dll` (Windows) / `libonnxruntime.so`
(Linux). Search order:

1. `CHRONOS_ORT_DIR` environment variable (directory containing the library,
   or the library file itself; the `<dir>/lib/` layout of the official release
   zip is also accepted),
2. `<executable dir>/ort/`,
3. `<executable dir>/runtime/ort/`,
4. plain system load (`onnxruntime.dll` / `libonnxruntime.so`).

The library is never unloaded: sessions hold function pointers into it.
