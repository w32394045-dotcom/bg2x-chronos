/*
 * Project Chronos — separable resampler implementation.
 */
#include "chronos/resample.h"

#include <algorithm>
#include <cmath>
#include <cstring>
#include <stdexcept>

#include "common.h"

namespace chronos {
namespace {

constexpr double kPi = 3.14159265358979323846;

double sinc(double x) noexcept {
  if (std::fabs(x) < 1e-12) return 1.0;
  const double px = kPi * x;
  return std::sin(px) / px;
}

/* Triangle (linear) kernel, support |x| < 1. */
double bilinear_kernel(double x) noexcept {
  const double ax = std::fabs(x);
  return ax < 1.0 ? 1.0 - ax : 0.0;
}

}  // namespace

double lanczos3_kernel(double x) noexcept {
  const double ax = std::fabs(x);
  if (ax >= 3.0) return 0.0;
  return sinc(x) * sinc(x / 3.0);
}

Resampler::Resampler(int src_w, int src_h, int dst_w, int dst_h, int channels,
                     ResampleFilter filter)
    : src_w_(src_w), src_h_(src_h), dst_w_(dst_w), dst_h_(dst_h), channels_(channels) {
  if (src_w <= 0 || src_h <= 0 || dst_w <= 0 || dst_h <= 0) {
    throw std::invalid_argument("Resampler: invalid dimensions");
  }
  if (channels < 1 || channels > 4) {
    throw std::invalid_argument("Resampler: invalid channel count");
  }
  identity_ = (src_w_ == dst_w_ && src_h_ == dst_h_);
  if (identity_) {
    h_offsets_.assign(static_cast<size_t>(dst_w_) + 1, 0);
    v_offsets_.assign(static_cast<size_t>(dst_h_) + 1, 0);
    scratch_floats_ = 0;
    return;
  }
  build_taps(src_w_, dst_w_, filter, &h_taps_, &h_offsets_);
  build_taps(src_h_, dst_h_, filter, &v_taps_, &v_offsets_);
  /* Horizontal pass writes dst_w x src_h x channels floats. */
  scratch_floats_ = static_cast<size_t>(dst_w_) * static_cast<size_t>(src_h_) *
                    static_cast<size_t>(channels_);
}

void Resampler::build_taps(int src_size, int dst_size, ResampleFilter filter,
                           std::vector<Tap>* taps, std::vector<int>* offsets) {
  taps->clear();
  offsets->clear();
  offsets->reserve(static_cast<size_t>(dst_size) + 1);

  const double ratio = static_cast<double>(src_size) / static_cast<double>(dst_size);
  /* Correct prefilter scaling: widen the kernel when shrinking. */
  const double filter_scale = ratio > 1.0 ? ratio : 1.0;
  const double support = (filter == ResampleFilter::kLanczos3 ? 3.0 : 1.0) * filter_scale;
  const double inv_scale = 1.0 / filter_scale;

  for (int i = 0; i < dst_size; ++i) {
    const double center = (static_cast<double>(i) + 0.5) * ratio - 0.5;
    int left = static_cast<int>(std::ceil(center - support));
    int right = static_cast<int>(std::floor(center + support));
    if (left > right) left = right;

    offsets->push_back(static_cast<int>(taps->size()));
    double total = 0.0;
    std::vector<Tap>& row = *taps;
    for (int j = left; j <= right; ++j) {
      const double distance = (center - static_cast<double>(j)) * inv_scale;
      double weight = (filter == ResampleFilter::kLanczos3) ? lanczos3_kernel(distance)
                                                            : bilinear_kernel(distance);
      if (weight == 0.0) continue;
      const int clamped = clamp_value(j, 0, src_size - 1);
      Tap tap;
      tap.index = clamped;
      tap.weight = static_cast<float>(weight);
      row.push_back(tap);
      total += weight;
    }
    if (total == 0.0) {
      /* Degenerate (can only happen for absurd scales): fall back to nearest. */
      Tap tap;
      tap.index = clamp_value(static_cast<int>(std::lround(center)), 0, src_size - 1);
      tap.weight = 1.0f;
      row.push_back(tap);
      total = 1.0;
    }
    /* Normalise so a constant input stays exactly constant. */
    const float normaliser = static_cast<float>(1.0 / total);
    for (size_t k = static_cast<size_t>(offsets->back()); k < row.size(); ++k) {
      row[k].weight *= normaliser;
    }
  }
  offsets->push_back(static_cast<int>(taps->size()));
}

void Resampler::run(const uint8_t* src, uint8_t* dst, float* scratch) const {
  const size_t dst_bytes = static_cast<size_t>(dst_w_) * static_cast<size_t>(dst_h_) *
                           static_cast<size_t>(channels_);
  if (identity_) {
    std::memcpy(dst, src, dst_bytes);
    return;
  }
  (void)dst_bytes;
  const int channels = channels_;
  const int dst_w = dst_w_;
  const int src_h = src_h_;
  const int src_w = src_w_;

  /* Horizontal pass: src (src_w x src_h) -> scratch (dst_w x src_h), float. */
  for (int y = 0; y < src_h; ++y) {
    const uint8_t* src_row = src + static_cast<size_t>(y) * static_cast<size_t>(src_w) *
                                       static_cast<size_t>(channels);
    float* dst_row = scratch + static_cast<size_t>(y) * static_cast<size_t>(dst_w) *
                                  static_cast<size_t>(channels);
    for (int x = 0; x < dst_w; ++x) {
      const int begin = h_offsets_[static_cast<size_t>(x)];
      const int end = h_offsets_[static_cast<size_t>(x) + 1];
      float* out = dst_row + static_cast<size_t>(x) * static_cast<size_t>(channels);
      for (int c = 0; c < channels; ++c) out[c] = 0.0f;
      for (int k = begin; k < end; ++k) {
        const Tap& tap = h_taps_[static_cast<size_t>(k)];
        const uint8_t* pixel =
            src_row + static_cast<size_t>(tap.index) * static_cast<size_t>(channels);
        for (int c = 0; c < channels; ++c) out[c] += static_cast<float>(pixel[c]) * tap.weight;
      }
    }
  }

  /* Vertical pass: scratch -> dst (dst_w x dst_h), quantised to 8-bit. */
  const int dst_h = dst_h_;
  for (int y = 0; y < dst_h; ++y) {
    const int begin = v_offsets_[static_cast<size_t>(y)];
    const int end = v_offsets_[static_cast<size_t>(y) + 1];
    uint8_t* dst_row = dst + static_cast<size_t>(y) * static_cast<size_t>(dst_w) *
                                 static_cast<size_t>(channels);
    for (int x = 0; x < dst_w; ++x) {
      const size_t offset = static_cast<size_t>(x) * static_cast<size_t>(channels);
      for (int c = 0; c < channels; ++c) {
        float value = 0.0f;
        for (int k = begin; k < end; ++k) {
          const Tap& tap = v_taps_[static_cast<size_t>(k)];
          value += scratch[static_cast<size_t>(tap.index) * static_cast<size_t>(dst_w) *
                               static_cast<size_t>(channels) +
                           offset + static_cast<size_t>(c)] *
                   tap.weight;
        }
        dst_row[offset + static_cast<size_t>(c)] = to_u8(value);
      }
    }
  }
}

void resample_image(const uint8_t* src, int src_w, int src_h, int channels, uint8_t* dst,
                    int dst_w, int dst_h, ResampleFilter filter) {
  Resampler resampler(src_w, src_h, dst_w, dst_h, channels, filter);
  std::vector<float> scratch(resampler.scratch_floats(), 0.0f);
  resampler.run(src, dst, scratch.empty() ? nullptr : scratch.data());
}

}  // namespace chronos
