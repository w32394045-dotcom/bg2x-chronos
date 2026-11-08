/*
 * Project Chronos — inference backend interface.
 *
 * Two implementations ship with the project:
 *   - stub backend (always built): high quality Lanczos3 / bilinear upscale,
 *     no model, no external dependency.
 *   - OpenVINO backend (only built with -DCHRONOS_WITH_OPENVINO=ON): loads an
 *     OpenVINO IR (.xml) or ONNX (.onnx) model and runs tiles through an
 *     N-deep asynchronous ov::InferRequest pipeline.
 *
 * The stub backend is driven tile-by-tile by the pipeline thread pool via
 * infer_tile() (thread-safe for distinct tiles). Backends that own their own
 * parallelism (OpenVINO) set supports_batch() == true and implement run_batch();
 * the pipeline then feeds them the whole plan and consumes patches through a
 * PatchSink, so per-inflight-request buffers stay small.
 */
#ifndef CHRONOS_BACKEND_H
#define CHRONOS_BACKEND_H

#include <atomic>
#include <cstddef>
#include <cstdint>
#include <memory>
#include <string>
#include <vector>

#include "chronos/image.h"
#include "chronos/options.h"
#include "chronos/tiling.h"

namespace chronos {

/* Per-worker scratch handed to infer_tile(); resized once, reused per tile. */
struct TileWorkspace {
  std::vector<float> resample_scratch;
};

/* Receives finished patches (in tile order of arrival). */
class PatchSink {
 public:
  virtual ~PatchSink() = default;

  /*
   * tile_index indexes the TilePlan, patch is patch_size x patch_size x 3
   * RGB bytes and remains valid only during the call.
   * Implementations must be thread-safe.
   */
  virtual void emit(int tile_index, const uint8_t* patch, int patch_size) = 0;
};

class IBackend {
 public:
  virtual ~IBackend() = default;

  /* CHRONOS_BACKEND_STUB / CHRONOS_BACKEND_OPENVINO. */
  virtual int id() const noexcept = 0;

  /* Scale factor this backend actually produces. */
  virtual int native_scale() const noexcept = 0;

  /* e.g. "stub/Lanczos3" or "openvino/GPU.0". */
  virtual std::string describe() const = 0;

  /* True when run_batch() must be used instead of infer_tile(). */
  virtual bool supports_batch() const noexcept { return false; }

  /*
   * Tile size the backend will actually consume. The stub backend returns the
   * requested value; OpenVINO returns its model's static input size so a model
   * exported for 128x128 tiles is fed 128x128 tiles regardless of the request.
   */
  virtual int tile_size_for(int requested_tile_size) const noexcept {
    (void)requested_tile_size;
    return requested_tile_size;
  }

  /*
   * Synchronous single-tile inference, called concurrently by pipeline workers
   * for distinct tiles. tile is tile_size x tile_size x 3 RGB; patch must hold
   * patch_size x patch_size x 3 RGB. Returns false and sets *err on failure.
   */
  virtual bool infer_tile(const uint8_t* tile, int tile_size, uint8_t* patch, int patch_size,
                          TileWorkspace& workspace, std::string* err) = 0;
  /*
   * Batched path (only when supports_batch()). Reads tiles from `src` using
   * `plan`, emits every patch through `sink`, polls `cancel` between tiles and
   * returns CHRONOS_OK / CHRONOS_ERR_INFER / CHRONOS_ERR_CANCELLED /
   * CHRONOS_ERR_BACKEND with *err set.
   */
  virtual int run_batch(const Image& src, const TilePlan& plan, PatchSink& sink,
                        const Options& options, const std::atomic<bool>& cancel,
                        std::string* err) {
    (void)src;
    (void)plan;
    (void)sink;
    (void)options;
    (void)cancel;
    if (err) *err = "backend does not implement a batch path";
    return CHRONOS_ERR_BACKEND;
  }
};

std::unique_ptr<IBackend> create_stub_backend(int scale, const Options& options,
                                              std::string* err);

/* Compile-time availability of the OpenVINO backend. */
bool openvino_available() noexcept;

/*
 * Returns nullptr and sets *err when the backend is not compiled in or the
 * model cannot be read/compiled. requested_scale <= 0 means "use the model's
 * native scale".
 */
std::unique_ptr<IBackend> create_openvino_backend(const std::string& model_path,
                                                  int requested_scale,
                                                  const Options& options, std::string* err);

}  // namespace chronos

#endif /* CHRONOS_BACKEND_H */
