/*
 * Project Chronos — feather blending implementation.
 */
#include "chronos/blending.h"

#include <algorithm>
#include <cmath>
#include <stdexcept>

#include "chronos/chronos.h"
#include "common.h"

namespace chronos {

std::vector<float> BlendAccumulator::axis_weights(int size) {
  std::vector<float> weights;
  if (size <= 0) return weights;
  weights.resize(static_cast<size_t>(size));
  for (int i = 0; i < size; ++i) {
    /* Raised-linear ramp with a 1 pixel floor at both ends. */
    const int rise = i + 1;
    const int fall = size - i;
    weights[static_cast<size_t>(i)] = static_cast<float>(rise < fall ? rise : fall);
  }
  return weights;
}

void BlendAccumulator::reset(int out_w, int out_h, int channels) {
  /* Patches handed to accumulate() are always RGB (inference runs on RGB). */
  if (out_w <= 0 || out_h <= 0 || channels != 3) {
    throw std::invalid_argument("BlendAccumulator::reset: invalid dimensions");
  }
  out_w_ = out_w;
  out_h_ = out_h;
  channels_ = channels;
  const size_t pixels = static_cast<size_t>(out_w) * static_cast<size_t>(out_h);
  accum_.assign(pixels * static_cast<size_t>(channels), 0.0f);
  weight_sum_.assign(pixels, 0.0f);
}

void BlendAccumulator::accumulate(const uint8_t* patch, int patch_w, int patch_h, int out_x,
                                  int out_y, const std::vector<float>& axis_weights) {
  if (patch == nullptr || patch_w <= 0 || patch_h <= 0) return;
  if (out_w_ <= 0 || out_h_ <= 0 || channels_ <= 0) return;
  if (static_cast<int>(axis_weights.size()) < std::max(patch_w, patch_h)) return;

  const int x_begin = std::max(0, -out_x);
  const int x_end = std::min(patch_w, out_w_ - out_x);
  const int y_begin = std::max(0, -out_y);
  const int y_end = std::min(patch_h, out_h_ - out_y);
  if (x_begin >= x_end || y_begin >= y_end) return;

  const int channels = channels_;
  const size_t out_w = static_cast<size_t>(out_w_);
  for (int py = y_begin; py < y_end; ++py) {
    const float wy = axis_weights[static_cast<size_t>(py)];
    const uint8_t* patch_row = patch + static_cast<size_t>(py) * static_cast<size_t>(patch_w) * 3;
    const size_t out_row = static_cast<size_t>(out_y + py) * out_w;
    for (int px = x_begin; px < x_end; ++px) {
      const float weight = wy * axis_weights[static_cast<size_t>(px)];
      const size_t pixel = out_row + static_cast<size_t>(out_x + px);
      const uint8_t* source = patch_row + static_cast<size_t>(px) * 3;
      float* destination = accum_.data() + pixel * static_cast<size_t>(channels);
      for (int c = 0; c < channels; ++c) {
        destination[c] += static_cast<float>(source[c]) * weight;
      }
      weight_sum_[pixel] += weight;
    }
  }
}

void BlendAccumulator::resolve(Image* out) const {
  if (out == nullptr) return;
  out->allocate(out_w_, out_h_, channels_);
  const size_t pixels = static_cast<size_t>(out_w_) * static_cast<size_t>(out_h_);
  const int channels = channels_;
  for (size_t pixel = 0; pixel < pixels; ++pixel) {
    const float sum = weight_sum_[pixel];
    const float inv = sum > 0.0f ? 1.0f / sum : 0.0f;
    const float* source = accum_.data() + pixel * static_cast<size_t>(channels);
    uint8_t* destination = out->pixels.data() + pixel * static_cast<size_t>(channels);
    for (int c = 0; c < channels; ++c) destination[c] = to_u8(source[c] * inv);
  }
}

}  // namespace chronos
