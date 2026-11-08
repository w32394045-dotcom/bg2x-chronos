/*
 * Project Chronos — pipeline implementation.
 *
 * Per image: decode -> tile plan -> per-tile extract (clamped padding) ->
 * parallel inference (thread pool, `options.threads` workers, hard cap 8) ->
 * feather blend into float accumulation buffers -> normalise -> encode.
 *
 * Progress counts finished tiles (`done / total`) atomically; the cancel flag is
 * polled at every tile boundary. All per-tile buffers are allocated once per
 * worker per image and reused for every tile - the tile hot loop does not
 * allocate.
 */
#include "chronos/pipeline.h"

#include <algorithm>
#include <atomic>
#include <cstring>
#include <exception>
#include <memory>
#include <mutex>
#include <new>
#include <string>
#include <vector>

#include "chronos/backend.h"
#include "chronos/blending.h"
#include "chronos/chronos.h"
#include "chronos/image.h"
#include "chronos/options.h"
#include "chronos/resample.h"
#include "chronos/tiling.h"
#include "common.h"
#include "thread_pool.h"

namespace chronos {
namespace {

/* Accumulates every finished patch into the shared blend buffer. */
class BlendingSink final : public PatchSink {
 public:
  BlendingSink(BlendAccumulator& blend, const TilePlan& plan, const std::vector<float>& axis,
               std::atomic<int>& done)
      : blend_(blend), plan_(plan), axis_(axis), done_(done) {}

  void emit(int tile_index, const uint8_t* patch, int patch_size) override {
    if (patch == nullptr || tile_index < 0 || tile_index >= plan_.count()) return;
    const Tile& tile = plan_.tiles[static_cast<size_t>(tile_index)];
    const int out_x = tile.x * plan_.scale;
    const int out_y = tile.y * plan_.scale;
    {
      std::lock_guard<std::mutex> lock(mutex_);
      blend_.accumulate(patch, patch_size, patch_size, out_x, out_y, axis_);
    }
    done_.fetch_add(1, std::memory_order_relaxed);
  }

 private:
  BlendAccumulator& blend_;
  const TilePlan& plan_;
  const std::vector<float>& axis_;
  std::atomic<int>& done_;
  std::mutex mutex_;
};

ResampleFilter filter_from_options(const Options& options) {
  return options.fast() ? ResampleFilter::kBilinear : ResampleFilter::kLanczos3;
}

/*
 * Extracts the alpha channel of a 4-channel image into a 1-channel image so it
 * can be scaled independently of the RGB inference path.
 */
bool extract_alpha(const Image& source, Image* alpha) {
  if (source.channels != 4) return false;
  alpha->allocate(source.width, source.height, 1);
  const size_t pixels = static_cast<size_t>(source.width) * static_cast<size_t>(source.height);
  const uint8_t* src = source.pixels.data();
  uint8_t* dst = alpha->pixels.data();
  for (size_t i = 0; i < pixels; ++i) dst[i] = src[i * 4 + 3];
  return true;
}

}  // namespace

struct Pipeline::Impl {
  Options options;
  int scale = 1;
  std::unique_ptr<IBackend> backend;
  std::unique_ptr<ThreadPool> pool;
  std::atomic<int> done{0};
  std::atomic<int> total{0};
  std::atomic<bool> cancel{false};
};

Pipeline::Pipeline() : impl_(new Impl()) {}

Pipeline::~Pipeline() = default;

std::unique_ptr<Pipeline> Pipeline::create(const std::string& model_path, int scale,
                                           const Options& options, std::string* err) {
  try {
    std::unique_ptr<Pipeline> pipeline(new Pipeline());
    Options normalized;
    if (normalize_options(options, &normalized, err) != CHRONOS_OK) return nullptr;
    pipeline->impl_->options = normalized;

    if (model_path.empty()) {
      if (scale < kMinStubScale || scale > kMaxStubScale) {
        set_error(err, "stub backend supports scale 1..4 only");
        return nullptr;
      }
      pipeline->impl_->scale = scale;
      pipeline->impl_->backend = create_stub_backend(scale, normalized, err);
    } else {
      pipeline->impl_->backend = create_openvino_backend(model_path, scale, normalized, err);
      if (pipeline->impl_->backend) {
        const int native_scale = pipeline->impl_->backend->native_scale();
        if (scale > 0 && native_scale > 0 && scale != native_scale) {
          set_error(err, "requested scale does not match the model scale (" +
                             std::to_string(native_scale) + ")");
          return nullptr;
        }
        pipeline->impl_->scale = native_scale > 0 ? native_scale : scale;
      }
    }
    if (!pipeline->impl_->backend) return nullptr;

    pipeline->impl_->pool =
        std::unique_ptr<ThreadPool>(new ThreadPool(normalized.effective_threads()));
    return pipeline;
  } catch (const std::exception& exception) {
    set_error(err, std::string("cannot create session: ") + exception.what());
  } catch (...) {
    set_error(err, "cannot create session: unknown error");
  }
  return nullptr;
}

int Pipeline::process(const std::string& in_path, const std::string& out_path, int* out_width,
                      int* out_height, std::string* err) {
  Impl& self = *impl_;
  self.done.store(0, std::memory_order_relaxed);
  self.total.store(0, std::memory_order_relaxed);
  if (out_width != nullptr) *out_width = 0;
  if (out_height != nullptr) *out_height = 0;
  if (self.cancel.load(std::memory_order_relaxed)) return CHRONOS_ERR_CANCELLED;

  Image source;
  const bool keep_alpha = self.options.preserve_alpha();
  int rc = load_image(in_path, keep_alpha, &source, err);
  if (rc != CHRONOS_OK) return rc;

  TilePlan plan;
  /* OpenVINO models dictate their own (static) tile size. */
  const int tile_size = self.backend->tile_size_for(self.options.tile_size);
  rc = plan_tiles(source.width, source.height, tile_size, self.options.overlap, self.scale,
                  &plan, err);
  if (rc != CHRONOS_OK) return rc;
  self.total.store(plan.count(), std::memory_order_relaxed);

  BlendAccumulator blend;
  try {
    blend.reset(plan.out_w, plan.out_h, 3);
  } catch (const std::bad_alloc&) {
    set_error(err, "out of memory allocating the blend canvas");
    return CHRONOS_ERR_UNKNOWN;
  }
  const std::vector<float> axis = BlendAccumulator::axis_weights(plan.patch_size);
  BlendingSink sink(blend, plan, axis, self.done);

  bool was_cancelled = false;
  bool infer_failed = false;
  std::string infer_error;
  try {
    if (self.backend->supports_batch()) {
      const int batch_rc =
          self.backend->run_batch(source, plan, sink, self.options, self.cancel, err);
      if (batch_rc != CHRONOS_OK) return batch_rc;
    } else {
      const int workers = self.pool->size();
      std::atomic<int> next_tile{0};
      std::atomic<bool> failed{false};
      std::atomic<bool> cancelled{false};
      std::vector<std::string> worker_errors(static_cast<size_t>(workers));

      auto worker = [&](int worker_index) {
        const size_t tile_pixels = static_cast<size_t>(plan.tile_size) *
                                   static_cast<size_t>(plan.tile_size);
        const size_t patch_pixels = static_cast<size_t>(plan.patch_size) *
                                    static_cast<size_t>(plan.patch_size);
        std::vector<uint8_t> tile_buffer(tile_pixels * 3);
        std::vector<uint8_t> patch_buffer(patch_pixels * 3);
        TileWorkspace workspace;

        for (;;) {
          if (self.cancel.load(std::memory_order_relaxed)) {
            cancelled.store(true);
            return;
          }
          if (failed.load(std::memory_order_relaxed)) return;
          const int tile_index = next_tile.fetch_add(1, std::memory_order_relaxed);
          if (tile_index >= plan.count()) return;
          const Tile& tile = plan.tiles[static_cast<size_t>(tile_index)];
          extract_tile_rgb(source, tile.x, tile.y, plan.tile_size, tile_buffer.data());
          std::string local_error;
          if (!self.backend->infer_tile(tile_buffer.data(), plan.tile_size, patch_buffer.data(),
                                        plan.patch_size, workspace, &local_error)) {
            worker_errors[static_cast<size_t>(worker_index)] =
                local_error.empty() ? "tile inference failed" : local_error;
            failed.store(true);
            return;
          }
          sink.emit(tile_index, patch_buffer.data(), plan.patch_size);
        }
      };

      self.pool->run(worker);

      was_cancelled = cancelled.load();
      infer_failed = failed.load();
      if (infer_failed) {
        for (const std::string& message : worker_errors) {
          if (!message.empty()) {
            infer_error = message;
            break;
          }
        }
      }
    }
  } catch (const std::bad_alloc&) {
    set_error(err, "out of memory while processing tiles");
    return CHRONOS_ERR_UNKNOWN;
  } catch (const std::exception& exception) {
    set_error(err, std::string("internal error while processing tiles: ") + exception.what());
    return CHRONOS_ERR_UNKNOWN;
  } catch (...) {
    set_error(err, "internal error while processing tiles");
    return CHRONOS_ERR_UNKNOWN;
  }

  if (was_cancelled) {
    set_error(err, "cancelled");
    return CHRONOS_ERR_CANCELLED;
  }
  if (infer_failed) {
    set_error(err, infer_error.empty() ? "tile inference failed" : infer_error);
    return CHRONOS_ERR_INFER;
  }

  Image rgb;
  blend.resolve(&rgb);

  Image output;
  if (keep_alpha) {
    Image alpha_source;
    Image alpha_scaled;
    if (!extract_alpha(source, &alpha_source)) {
      set_error(err, "preserve_alpha requested but the source has no alpha channel");
      return CHRONOS_ERR_PARAM;
    }
    try {
      const ResampleFilter filter = filter_from_options(self.options);
      Resampler alpha_resampler(source.width, source.height, plan.out_w, plan.out_h, 1, filter);
      alpha_scaled.allocate(plan.out_w, plan.out_h, 1);
      std::vector<float> scratch(alpha_resampler.scratch_floats());
      alpha_resampler.run(alpha_source.pixels.data(), alpha_scaled.pixels.data(),
                          scratch.empty() ? nullptr : scratch.data());
      output.allocate(plan.out_w, plan.out_h, 4);
      const size_t pixels =
          static_cast<size_t>(plan.out_w) * static_cast<size_t>(plan.out_h);
      for (size_t i = 0; i < pixels; ++i) {
        output.pixels[i * 4 + 0] = rgb.pixels[i * 3 + 0];
        output.pixels[i * 4 + 1] = rgb.pixels[i * 3 + 1];
        output.pixels[i * 4 + 2] = rgb.pixels[i * 3 + 2];
        output.pixels[i * 4 + 3] = alpha_scaled.pixels[i];
      }
    } catch (const std::bad_alloc&) {
      set_error(err, "out of memory while compositing alpha");
      return CHRONOS_ERR_UNKNOWN;
    } catch (const std::exception& exception) {
      set_error(err, std::string("cannot scale alpha channel: ") + exception.what());
      return CHRONOS_ERR_UNKNOWN;
    }
  } else {
    output = std::move(rgb);
  }

  rc = save_image(out_path, output, err);
  if (rc != CHRONOS_OK) return rc;

  if (out_width != nullptr) *out_width = output.width;
  if (out_height != nullptr) *out_height = output.height;
  return CHRONOS_OK;
}

int Pipeline::backend_id() const noexcept {
  return impl_->backend ? impl_->backend->id() : CHRONOS_BACKEND_NONE;
}

int Pipeline::scale() const noexcept { return impl_->scale; }

std::string Pipeline::describe() const {
  return impl_->backend ? impl_->backend->describe() : std::string("none");
}

int Pipeline::progress_done() const noexcept {
  return impl_->done.load(std::memory_order_relaxed);
}

int Pipeline::progress_total() const noexcept {
  return impl_->total.load(std::memory_order_relaxed);
}

void Pipeline::reset_progress() noexcept {
  impl_->done.store(0, std::memory_order_relaxed);
  impl_->total.store(0, std::memory_order_relaxed);
}

void Pipeline::cancel() noexcept { impl_->cancel.store(true, std::memory_order_relaxed); }

bool Pipeline::cancelled() const noexcept {
  return impl_->cancel.load(std::memory_order_relaxed);
}

}  // namespace chronos
