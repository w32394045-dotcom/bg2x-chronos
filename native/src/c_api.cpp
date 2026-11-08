/*
 * Project Chronos — C ABI implementation for native/include/chronos/chronos.h.
 *
 * Every entry point is wrapped in try/catch(...): no C++ exception may cross the
 * frozen Go <-> C++ boundary. Invalid arguments return CHRONOS_ERR_PARAM (-4),
 * error buffers are always NUL-terminated and the thread-local last-error string
 * is updated on every failure path.
 */
#include <chronos/chronos.h>

#include <cstddef>
#include <cstring>
#include <memory>
#include <new>
#include <string>

#include "chronos/backend.h"
#include "chronos/image.h"
#include "chronos/options.h"
#include "chronos/pipeline.h"
#include "common.h"

#ifndef CHRONOS_NATIVE_VERSION
#define CHRONOS_NATIVE_VERSION "1.0.0"
#endif

/* Opaque C handle owning one Pipeline (one per Go worker). */
struct chronos_session {
  std::unique_ptr<chronos::Pipeline> pipeline;
};

namespace {

/* Thread-local last error (never NULL, empty when no error on this thread). */
thread_local std::string g_last_error;

void clear_last_error() { g_last_error.clear(); }

void set_last_error(const std::string& message) { g_last_error = message; }

int fail(char* err, int err_len, std::string message) {
  set_last_error(message);
  chronos::copy_error(err, err_len, message);
  return CHRONOS_ERR_PARAM;
}

int fail_with_code(char* err, int err_len, int code, const std::string& message) {
  set_last_error(message);
  chronos::copy_error(err, err_len, message);
  return code;
}

void prepare_error_buffer(char* err, int err_len) {
  if (err != nullptr && err_len > 0) err[0] = '\0';
}

std::string take_pipeline_error(std::string* error) {
  return error->empty() ? std::string("native pipeline failed") : *error;
}

chronos::Options options_from_c(const chronos_options* options, std::string* error) {
  chronos::Options result;
  if (options == nullptr) return result;
  if (options->struct_size != 0 &&
      options->struct_size != static_cast<int>(sizeof(chronos_options))) {
    set_error(error, "chronos_options.struct_size must be sizeof(chronos_options)");
    return result;
  }
  for (int i = 0; i < 4; ++i) {
    if (options->reserved[i] != 0) {
      set_error(error, "chronos_options.reserved must be zeroed");
      return result;
    }
  }
  result.tile_size = options->tile_size;
  result.overlap = options->overlap;
  result.threads = options->threads;
  result.device_index = options->device_index;
  result.flags = options->flags;
  return result;
}

}  // namespace

extern "C" {

int chronos_abi_version(void) { return CHRONOS_ABI_VERSION; }

const char* chronos_version_string(void) {
  /* String literal: no allocation, no exception. */
  return "chronos-native " CHRONOS_NATIVE_VERSION " (abi " "1" ")";
}

const char* chronos_backend_name(int backend) {
  switch (backend) {
    case CHRONOS_BACKEND_NONE:
      return "none";
    case CHRONOS_BACKEND_STUB:
      return "stub";
    case CHRONOS_BACKEND_OPENVINO:
      return "openvino";
    default:
      set_last_error("unknown backend id passed to chronos_backend_name");
      return "unknown";
  }
}

int chronos_backend_available(int backend) {
  try {
    switch (backend) {
      case CHRONOS_BACKEND_STUB:
        return 1;
      case CHRONOS_BACKEND_OPENVINO:
        return chronos::openvino_available() ? 1 : 0;
      case CHRONOS_BACKEND_NONE:
      default:
        return 0;
    }
  } catch (...) {
    set_last_error("chronos_backend_available: unexpected error");
    return 0;
  }
}

const char* chronos_last_error(void) {
  /* c_str() never throws and stays valid until this thread calls the API again. */
  return g_last_error.c_str();
}

chronos_session* chronos_session_create(const char* model_path, int scale,
                                        const chronos_options* options, char* err,
                                        int err_len) {
  clear_last_error();
  prepare_error_buffer(err, err_len);
  try {
    std::string error;
    const chronos::Options converted = options_from_c(options, &error);
    if (!error.empty()) {
      set_last_error(error);
      chronos::copy_error(err, err_len, error);
      return nullptr;
    }
    const std::string model = model_path != nullptr ? std::string(model_path) : std::string();
    std::unique_ptr<chronos::Pipeline> pipeline =
        chronos::Pipeline::create(model, scale, converted, &error);
    if (!pipeline) {
      const std::string message = take_pipeline_error(&error);
      set_last_error(message);
      chronos::copy_error(err, err_len, message);
      return nullptr;
    }
    std::unique_ptr<chronos_session> session(new (std::nothrow) chronos_session());
    if (!session) {
      const std::string message = "out of memory creating chronos_session";
      set_last_error(message);
      chronos::copy_error(err, err_len, message);
      return nullptr;
    }
    session->pipeline = std::move(pipeline);
    return session.release();
  } catch (const std::bad_alloc&) {
    const std::string message = "out of memory creating chronos_session";
    set_last_error(message);
    chronos::copy_error(err, err_len, message);
    return nullptr;
  } catch (const std::exception& exception) {
    const std::string message = std::string("unexpected error creating session: ") +
                                exception.what();
    set_last_error(message);
    chronos::copy_error(err, err_len, message);
    return nullptr;
  } catch (...) {
    const std::string message = "unexpected error creating session";
    set_last_error(message);
    chronos::copy_error(err, err_len, message);
    return nullptr;
  }
}

void chronos_session_free(chronos_session* session) {
  try {
    delete session;
  } catch (...) {
    /* Deleting must never throw; swallow anything unexpected. */
  }
}

int chronos_session_backend(const chronos_session* session) {
  try {
    if (session == nullptr || !session->pipeline) {
      set_last_error("chronos_session_backend: session is NULL");
      return CHRONOS_ERR_PARAM;
    }
    return session->pipeline->backend_id();
  } catch (...) {
    set_last_error("chronos_session_backend: unexpected error");
    return CHRONOS_ERR_UNKNOWN;
  }
}

int chronos_session_scale(const chronos_session* session) {
  try {
    if (session == nullptr || !session->pipeline) {
      set_last_error("chronos_session_scale: session is NULL");
      return CHRONOS_ERR_PARAM;
    }
    return session->pipeline->scale();
  } catch (...) {
    set_last_error("chronos_session_scale: unexpected error");
    return CHRONOS_ERR_UNKNOWN;
  }
}

int chronos_session_describe(const chronos_session* session, char* buf, int buf_len) {
  try {
    if (session == nullptr || !session->pipeline) {
      set_last_error("chronos_session_describe: session is NULL");
      return CHRONOS_ERR_PARAM;
    }
    const std::string description = session->pipeline->describe();
    if (buf == nullptr || buf_len <= 0) return static_cast<int>(description.size());
    const size_t capacity = static_cast<size_t>(buf_len);
    const size_t count =
        description.size() < capacity - 1 ? description.size() : capacity - 1;
    std::memcpy(buf, description.data(), count);
    buf[count] = '\0';
    return static_cast<int>(count);
  } catch (...) {
    set_last_error("chronos_session_describe: unexpected error");
    return CHRONOS_ERR_UNKNOWN;
  }
}

int chronos_process(chronos_session* session, const char* in_path, const char* out_path,
                    int* out_width, int* out_height, char* err, int err_len) {
  clear_last_error();
  prepare_error_buffer(err, err_len);
  try {
    if (session == nullptr || !session->pipeline) {
      return fail(err, err_len, "chronos_process: session is NULL");
    }
    if (in_path == nullptr || in_path[0] == '\0') {
      return fail(err, err_len, "chronos_process: input path is NULL or empty");
    }
    if (out_path == nullptr || out_path[0] == '\0') {
      return fail(err, err_len, "chronos_process: output path is NULL or empty");
    }
    std::string error;
    int width = 0;
    int height = 0;
    const int rc = session->pipeline->process(in_path, out_path, &width, &height, &error);
    if (rc != CHRONOS_OK) {
      return fail_with_code(err, err_len, rc, take_pipeline_error(&error));
    }
    if (out_width != nullptr) *out_width = width;
    if (out_height != nullptr) *out_height = height;
    return CHRONOS_OK;
  } catch (const std::bad_alloc&) {
    return fail_with_code(err, err_len, CHRONOS_ERR_UNKNOWN,
                          "out of memory in chronos_process");
  } catch (const std::exception& exception) {
    return fail_with_code(err, err_len, CHRONOS_ERR_UNKNOWN,
                          std::string("unexpected error in chronos_process: ") +
                              exception.what());
  } catch (...) {
    return fail_with_code(err, err_len, CHRONOS_ERR_UNKNOWN,
                          "unexpected error in chronos_process");
  }
}

void chronos_session_progress(const chronos_session* session, int* done, int* total) {
  int current_done = 0;
  int current_total = 0;
  try {
    if (session != nullptr && session->pipeline) {
      current_done = session->pipeline->progress_done();
      current_total = session->pipeline->progress_total();
    }
  } catch (...) {
    current_done = 0;
    current_total = 0;
  }
  if (done != nullptr) *done = current_done;
  if (total != nullptr) *total = current_total;
}

void chronos_session_reset_progress(chronos_session* session) {
  try {
    if (session != nullptr && session->pipeline) session->pipeline->reset_progress();
  } catch (...) {
    /* progress reset must never propagate */
  }
}

void chronos_session_cancel(chronos_session* session) {
  try {
    if (session != nullptr && session->pipeline) session->pipeline->cancel();
  } catch (...) {
    /* cancellation must never propagate */
  }
}

int chronos_session_cancelled(const chronos_session* session) {
  try {
    if (session == nullptr || !session->pipeline) return 0;
    return session->pipeline->cancelled() ? 1 : 0;
  } catch (...) {
    return 0;
  }
}

}  // extern "C"
