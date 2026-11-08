/*
 * Project Chronos — stub backend (no model, high quality resampling).
 *
 * The stub backend exists so the whole tiling/blending/progress/cancel
 * machinery can be exercised (and shipped) without an AI model. Per tile it
 * performs a separable Lanczos3 resize (bilinear with CHRONOS_FLAG_FAST) from
 * tile_size x tile_size to (tile_size * scale)^2. The resampler tap tables are
 * built once at session creation, so the per-tile hot loop performs no heap
 * allocation.
 */
#include <memory>
#include <stdexcept>
#include <string>

#include "chronos/backend.h"
#include "chronos/chronos.h"
#include "chronos/options.h"
#include "chronos/resample.h"
#include "common.h"

namespace chronos {
namespace {

class StubBackend final : public IBackend {
 public:
  StubBackend(int scale, const Options& options)
      : scale_(scale),
        tile_size_(options.tile_size),
        patch_size_(options.tile_size * scale),
        filter_(options.fast() ? ResampleFilter::kBilinear : ResampleFilter::kLanczos3),
        resampler_(options.tile_size, options.tile_size, options.tile_size * scale,
                   options.tile_size * scale, 3, filter_) {}

  int id() const noexcept override { return CHRONOS_BACKEND_STUB; }

  int native_scale() const noexcept override { return scale_; }

  std::string describe() const override {
    return filter_ == ResampleFilter::kLanczos3 ? "stub/Lanczos3" : "stub/Bilinear";
  }

  bool supports_batch() const noexcept override { return false; }

  bool infer_tile(const uint8_t* tile, int tile_size, uint8_t* patch, int patch_size,
                  TileWorkspace& workspace, std::string* err) override {
    if (tile == nullptr || patch == nullptr) {
      set_error(err, "stub backend: null tile buffer");
      return false;
    }
    if (tile_size != tile_size_ || patch_size != patch_size_) {
      set_error(err, "stub backend: tile/patch size mismatch");
      return false;
    }
    /* Scratch is allocated once per worker (first tile) and then reused. */
    const size_t needed = resampler_.scratch_floats();
    if (workspace.resample_scratch.size() < needed) {
      workspace.resample_scratch.resize(needed);
    }
    resampler_.run(tile, patch, needed == 0 ? nullptr : workspace.resample_scratch.data());
    return true;
  }

 private:
  int scale_ = 1;
  int tile_size_ = 0;
  int patch_size_ = 0;
  ResampleFilter filter_ = ResampleFilter::kLanczos3;
  Resampler resampler_;
};

}  // namespace

std::unique_ptr<IBackend> create_stub_backend(int scale, const Options& options,
                                              std::string* err) {
  if (scale < kMinStubScale || scale > kMaxStubScale) {
    set_error(err, "stub backend supports scale 1..4 only");
    return nullptr;
  }
  try {
    return std::unique_ptr<IBackend>(new StubBackend(scale, options));
  } catch (const std::exception& exception) {
    set_error(err, std::string("cannot create stub backend: ") + exception.what());
  } catch (...) {
    set_error(err, "cannot create stub backend: unknown error");
  }
  return nullptr;
}

}  // namespace chronos
