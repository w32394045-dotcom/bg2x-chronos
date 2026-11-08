/*
 * Project Chronos — stable C ABI between Go (cgo) and the C++ inference core.
 * ABI version: 1. Any signature change requires bumping CHRONOS_ABI_VERSION and
 * updating docs/CONTRACTS.md.
 *
 * Rules:
 *  - No C++ exception may cross this boundary. Every entry point catches everything.
 *  - Sessions are NOT thread-safe: one session per worker thread.
 *  - Pixel data never crosses this boundary; only file paths.
 */
#ifndef CHRONOS_CHRONOS_H
#define CHRONOS_CHRONOS_H

#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

#define CHRONOS_ABI_VERSION 1

/* Return codes (shared with Go and documented in docs/CONTRACTS.md) */
#define CHRONOS_OK 0
#define CHRONOS_ERR_IO (-1)
#define CHRONOS_ERR_MODEL (-2)
#define CHRONOS_ERR_INFER (-3)
#define CHRONOS_ERR_PARAM (-4)
#define CHRONOS_ERR_BACKEND (-5)
#define CHRONOS_ERR_CANCELLED (-6)
#define CHRONOS_ERR_UNKNOWN (-99)

/* Backend identifiers */
#define CHRONOS_BACKEND_NONE 0
#define CHRONOS_BACKEND_STUB 1
#define CHRONOS_BACKEND_OPENVINO 2

/* Flags */
#define CHRONOS_FLAG_NONE 0
#define CHRONOS_FLAG_FAST (1 << 0)          /* bilinear resample instead of Lanczos in stub */
#define CHRONOS_FLAG_PRESERVE_ALPHA (1 << 1) /* keep alpha channel end-to-end */

typedef struct chronos_options {
    /* Must be sizeof(chronos_options); 0 is rejected by create(). */
    int struct_size;
    int tile_size;    /* <= 0 => 256 */
    int overlap;      /* <= 0 => 16; must be < tile_size */
    int threads;      /* <= 0 => hardware_concurrency */
    int device_index; /* OpenVINO device index, 0 default */
    int flags;        /* CHRONOS_FLAG_* */
    int reserved[4];  /* must be zeroed */
} chronos_options;

typedef struct chronos_session chronos_session;

/* --- introspection ------------------------------------------------------- */
int chronos_abi_version(void);
const char* chronos_version_string(void);
const char* chronos_backend_name(int backend);
int chronos_backend_available(int backend);
/* Thread-local last error message (never NULL, empty string when no error). */
const char* chronos_last_error(void);

/* --- session lifecycle --------------------------------------------------- */
/*
 * model_path: empty/NULL => stub backend (high quality resample, no AI model).
 *             otherwise tries OpenVINO IR (.xml) or ONNX (.onnx) when built with
 *             CHRONOS_WITH_OPENVINO; returns NULL on failure.
 * scale:      model native scale, or desired output scale for the stub backend.
 * options:    NULL => defaults.
 * err/err_len: optional caller buffer for a human readable message.
 */
chronos_session* chronos_session_create(const char* model_path, int scale,
                                        const chronos_options* options,
                                        char* err, int err_len);
void chronos_session_free(chronos_session* session);

int chronos_session_backend(const chronos_session* session);
int chronos_session_scale(const chronos_session* session);
/* Writes e.g. "openvino/GPU" or "stub/Lanczos3"; returns length, or negative code. */
int chronos_session_describe(const chronos_session* session, char* buf, int buf_len);

/* --- per-image execution ------------------------------------------------- */
/*
 * Reads in_path, upscales with tiling + feather blending, writes out_path.
 * out_width/out_height are optional. Returns CHRONOS_OK or a negative code.
 */
int chronos_process(chronos_session* session, const char* in_path, const char* out_path,
                    int* out_width, int* out_height, char* err, int err_len);

/* --- progress / cancellation -------------------------------------------- */
/* done/total counted in tiles of the image currently processed. */
void chronos_session_progress(const chronos_session* session, int* done, int* total);
void chronos_session_reset_progress(chronos_session* session);
void chronos_session_cancel(chronos_session* session);
int chronos_session_cancelled(const chronos_session* session);

#ifdef __cplusplus
} /* extern "C" */
#endif

#endif /* CHRONOS_CHRONOS_H */
