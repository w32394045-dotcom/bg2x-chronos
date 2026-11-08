/*
 * Project Chronos — resampler tests.
 *
 * Covers: kernel shape, output dimensions, exact constant preservation (both
 * filters), identity at scale 1, 2x upscale of a known gradient, downscale with
 * prefilter scaling, and alpha/4-channel handling.
 */
#include <chronos/resample.h>

#include <cmath>
#include <cstddef>
#include <exception>
#include <vector>

#include "harness.h"

namespace {

std::vector<uint8_t> constant_buffer(int width, int height, int channels, uint8_t value) {
  return std::vector<uint8_t>(static_cast<size_t>(width) * height * channels, value);
}

void check_constant(const std::vector<uint8_t>& buffer, uint8_t expected) {
  for (size_t i = 0; i < buffer.size(); ++i) {
    if (buffer[i] != expected) {
      CHECK_EQ(static_cast<int>(buffer[i]), static_cast<int>(expected));
    }
  }
}

}  // namespace

TEST(resample, lanczos3_kernel_shape) {
  CHECK_NEAR(chronos::lanczos3_kernel(0.0), 1.0, 1e-12);
  CHECK_NEAR(chronos::lanczos3_kernel(1.0), 0.0, 1e-9);
  CHECK_NEAR(chronos::lanczos3_kernel(-1.0), 0.0, 1e-9);
  CHECK_NEAR(chronos::lanczos3_kernel(2.0), 0.0, 1e-9);
  CHECK_NEAR(chronos::lanczos3_kernel(3.0), 0.0, 1e-12);
  CHECK_NEAR(chronos::lanczos3_kernel(4.0), 0.0, 1e-12);
  CHECK_NEAR(chronos::lanczos3_kernel(0.5), chronos::lanczos3_kernel(-0.5), 1e-12);
  /* Positive main lobe, negative first side lobe. */
  CHECK(chronos::lanczos3_kernel(0.5) > 0.0);
  CHECK(chronos::lanczos3_kernel(1.5) < 0.0);
}

TEST(resample, output_dimensions_both_filters) {
  const int src_w = 9;
  const int src_h = 7;
  const std::vector<uint8_t> source = constant_buffer(src_w, src_h, 3, 123);
  for (chronos::ResampleFilter filter :
       {chronos::ResampleFilter::kLanczos3, chronos::ResampleFilter::kBilinear}) {
    std::vector<uint8_t> destination(37 * 29 * 3);
    chronos::resample_image(source.data(), src_w, src_h, 3, destination.data(), 37, 29, filter);
    check_constant(destination, 123);
  }
}

TEST(resample, constant_preserved_when_downscaling) {
  const std::vector<uint8_t> source = constant_buffer(16, 12, 3, 200);
  for (chronos::ResampleFilter filter :
       {chronos::ResampleFilter::kLanczos3, chronos::ResampleFilter::kBilinear}) {
    std::vector<uint8_t> destination(8 * 6 * 3);
    chronos::resample_image(source.data(), 16, 12, 3, destination.data(), 8, 6, filter);
    check_constant(destination, 200);
  }
}

TEST(resample, identity_at_scale_one_is_bit_exact) {
  const int width = 8;
  const int height = 8;
  std::vector<uint8_t> source(static_cast<size_t>(width) * height * 3);
  for (size_t i = 0; i < source.size(); ++i) source[i] = static_cast<uint8_t>((i * 37) % 251);
  for (chronos::ResampleFilter filter :
       {chronos::ResampleFilter::kLanczos3, chronos::ResampleFilter::kBilinear}) {
    std::vector<uint8_t> destination(source.size(), 0);
    chronos::resample_image(source.data(), width, height, 3, destination.data(), width, height,
                            filter);
    CHECK(source == destination);
  }
}

TEST(resample, two_times_upscale_of_known_gradient) {
  /* Source value = x * 30 (0..210). At 2x the mapped source coordinate of
   * destination pixel x is (x + 0.5) / 2 - 0.5, so the expected value is
   * that coordinate times 30 (clamped by the replicated border). */
  const int src_w = 8;
  const int src_h = 8;
  std::vector<uint8_t> source(static_cast<size_t>(src_w) * src_h * 3);
  for (int y = 0; y < src_h; ++y) {
    for (int x = 0; x < src_w; ++x) {
      uint8_t* pixel = source.data() + (static_cast<size_t>(y) * src_w + x) * 3;
      pixel[0] = static_cast<uint8_t>(x * 30);
      pixel[1] = static_cast<uint8_t>(x * 30);
      pixel[2] = static_cast<uint8_t>(x * 30);
    }
  }

  for (chronos::ResampleFilter filter :
       {chronos::ResampleFilter::kLanczos3, chronos::ResampleFilter::kBilinear}) {
    const int dst_w = 16;
    const int dst_h = 16;
    std::vector<uint8_t> destination(static_cast<size_t>(dst_w) * dst_h * 3);
    chronos::resample_image(source.data(), src_w, src_h, 3, destination.data(), dst_w, dst_h,
                            filter);

    for (int y = 0; y < dst_h; ++y) {
      int previous = -1;
      for (int x = 0; x < dst_w; ++x) {
        const int value = destination[(static_cast<size_t>(y) * dst_w + x) * 3];
        const double coordinate = (static_cast<double>(x) + 0.5) / 2.0 - 0.5;
        const double expected = coordinate * 30.0;
        CHECK_NEAR(value, expected, 13.0);
        /* Monotonic non-decreasing along a ramp (allow 2 LSB of edge ringing). */
        CHECK(value >= previous - 2);
        previous = value;
      }
    }
    /* Rows are identical because the source is horizontally constant per row. */
    for (int y = 1; y < dst_h; ++y) {
      for (int x = 0; x < dst_w; ++x) {
        CHECK_EQ(destination[(static_cast<size_t>(y) * dst_w + x) * 3],
                 destination[(static_cast<size_t>(x)) * 3]);
      }
    }
  }
}

TEST(resample, downscale_of_gradient_stays_monotonic) {
  const int src_w = 16;
  const int src_h = 4;
  std::vector<uint8_t> source(static_cast<size_t>(src_w) * src_h);
  for (int y = 0; y < src_h; ++y) {
    for (int x = 0; x < src_w; ++x) {
      source[static_cast<size_t>(y) * src_w + x] = static_cast<uint8_t>(x * 15);
    }
  }
  std::vector<uint8_t> destination(8 * 2);
  chronos::resample_image(source.data(), src_w, src_h, 1, destination.data(), 8, 2,
                          chronos::ResampleFilter::kLanczos3);
  for (int y = 0; y < 2; ++y) {
    for (int x = 1; x < 8; ++x) {
      CHECK(destination[static_cast<size_t>(y) * 8 + x] + 2 >=
            destination[static_cast<size_t>(y) * 8 + x - 1]);
    }
  }
}

TEST(resample, four_channel_alpha_is_preserved) {
  const int src_w = 6;
  const int src_h = 5;
  std::vector<uint8_t> source(static_cast<size_t>(src_w) * src_h * 4);
  for (size_t i = 0; i < static_cast<size_t>(src_w) * src_h; ++i) {
    source[i * 4 + 0] = 10;
    source[i * 4 + 1] = 20;
    source[i * 4 + 2] = 30;
    source[i * 4 + 3] = 200;
  }
  std::vector<uint8_t> destination(12 * 10 * 4);
  chronos::resample_image(source.data(), src_w, src_h, 4, destination.data(), 12, 10,
                          chronos::ResampleFilter::kLanczos3);
  for (size_t i = 0; i < static_cast<size_t>(12) * 10; ++i) {
    CHECK_EQ(static_cast<int>(destination[i * 4 + 0]), 10);
    CHECK_EQ(static_cast<int>(destination[i * 4 + 1]), 20);
    CHECK_EQ(static_cast<int>(destination[i * 4 + 2]), 30);
    CHECK_EQ(static_cast<int>(destination[i * 4 + 3]), 200);
  }
}

TEST(resample, rejects_invalid_dimensions) {
  bool threw = false;
  try {
    chronos::Resampler invalid(0, 5, 10, 10, 3, chronos::ResampleFilter::kLanczos3);
  } catch (const std::exception&) {
    threw = true;
  }
  CHECK(threw);

  threw = false;
  try {
    chronos::Resampler invalid(5, 5, 10, 10, 5, chronos::ResampleFilter::kLanczos3);
  } catch (const std::exception&) {
    threw = true;
  }
  CHECK(threw);
}
