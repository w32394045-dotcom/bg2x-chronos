/*
 * Project Chronos — feather blending.
 *
 * Per-axis weight is a raised linear ramp with a floor of 1 at both ends:
 *
 *     w(i) = max(1, min(i + 1, size - i))       i in [0, size)
 *
 * i.e. 1, 2, 3, ... plateau ..., 3, 2, 1. The floor keeps the weights strictly
 * positive so no pixel can end up with a zero weight sum (no division by zero),
 * and in the overlap of two neighbouring tiles the two ramps sum to a constant
 * (overlap * scale + 1 per axis) because the tiles are offset by exactly
 * step * scale in scaled coordinates. Blending therefore is a weighted average,
 * not a hard stitch: a constant input stays bit-exact constant and gradients
 * stay continuous across tile borders (docs/CONTRACTS.md section 2).
 */
#ifndef CHRONOS_BLENDING_H
#define CHRONOS_BLENDING_H

#include <cstdint>
#include <vector>

#include "chronos/image.h"

namespace chronos {

class BlendAccumulator {
 public:
  BlendAccumulator() = default;

  /*
   * Resets to a zeroed accumulator of out_w x out_h RGB (3 channels) float
   * buffers. Patches fed to accumulate() are always 3-channel RGB, matching the
   * inference path (alpha is handled separately by the pipeline).
   */
  void reset(int out_w, int out_h, int channels);

  /*
   * Adds one upscaled patch (patch_w x patch_h x channels, 8-bit) at output
   * position (out_x, out_y). `axis_weights` must hold patch_w weights for the
   * horizontal axis; the vertical weight is looked up by row index, so
   * axis_weights must also be valid for patch_h indices (all patches of a plan
   * are square, so one table is enough).
   * Pixels falling outside the canvas are clipped.
   */
  void accumulate(const uint8_t* patch, int patch_w, int patch_h, int out_x, int out_y,
                  const std::vector<float>& axis_weights);

  /* Divides by the accumulated weight, rounds and clamps into an 8-bit image. */
  void resolve(Image* out) const;

  int width() const noexcept { return out_w_; }
  int height() const noexcept { return out_h_; }
  int channels() const noexcept { return channels_; }

  /* Read-only access for diagnostics/tests: normalisation denominators. */
  const std::vector<float>& weight_sum() const noexcept { return weight_sum_; }

  /* Raised linear ramp used on both axes. */
  static std::vector<float> axis_weights(int size);

 private:
  int out_w_ = 0;
  int out_h_ = 0;
  int channels_ = 0;
  std::vector<float> accum_;      /* out_w * out_h * channels */
  std::vector<float> weight_sum_; /* out_w * out_h */
};

}  // namespace chronos

#endif /* CHRONOS_BLENDING_H */
