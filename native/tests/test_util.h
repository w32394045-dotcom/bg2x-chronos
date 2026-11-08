/*
 * Project Chronos — shared test helpers (images, temp files, comparisons).
 */
#ifndef CHRONOS_TEST_UTIL_H
#define CHRONOS_TEST_UTIL_H

#include <chronos/chronos.h>
#include <chronos/image.h>

#include <algorithm>
#include <cstddef>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <string>
#include <vector>

#include "harness.h"

namespace chronos_test {

/* Files land in the ctest working directory (the build tree). */
inline std::string temp_path(const std::string& name) { return "chronos_test_" + name; }

inline chronos::Image make_gradient_rgb(int width, int height) {
  chronos::Image image;
  image.allocate(width, height, 3);
  for (int y = 0; y < height; ++y) {
    for (int x = 0; x < width; ++x) {
      uint8_t* pixel = image.row(y) + static_cast<size_t>(x) * 3;
      pixel[0] = static_cast<uint8_t>((x * 255) / (width > 1 ? width - 1 : 1));
      pixel[1] = static_cast<uint8_t>((y * 255) / (height > 1 ? height - 1 : 1));
      pixel[2] = static_cast<uint8_t>((x + y) % 256);
    }
  }
  return image;
}

inline chronos::Image make_constant_rgb(int width, int height, uint8_t r, uint8_t g, uint8_t b) {
  chronos::Image image;
  image.allocate(width, height, 3);
  for (int y = 0; y < height; ++y) {
    for (int x = 0; x < width; ++x) {
      uint8_t* pixel = image.row(y) + static_cast<size_t>(x) * 3;
      pixel[0] = r;
      pixel[1] = g;
      pixel[2] = b;
    }
  }
  return image;
}

inline chronos::Image make_gradient_rgba(int width, int height) {
  chronos::Image image;
  image.allocate(width, height, 4);
  for (int y = 0; y < height; ++y) {
    for (int x = 0; x < width; ++x) {
      uint8_t* pixel = image.row(y) + static_cast<size_t>(x) * 4;
      pixel[0] = static_cast<uint8_t>((x * 255) / (width > 1 ? width - 1 : 1));
      pixel[1] = static_cast<uint8_t>((y * 255) / (height > 1 ? height - 1 : 1));
      pixel[2] = 64;
      pixel[3] = static_cast<uint8_t>(32 + (x * 200) / (width > 1 ? width - 1 : 1));
    }
  }
  return image;
}

inline void write_image_or_fail(const std::string& path, const chronos::Image& image) {
  std::string error;
  const int rc = chronos::save_image(path, image, &error);
  CHECK_EQ(rc, CHRONOS_OK);
  CHECK(error.empty());
}

inline chronos::Image read_image_or_fail(const std::string& path, bool preserve_alpha) {
  chronos::Image image;
  std::string error;
  const int rc = chronos::load_image(path, preserve_alpha, &image, &error);
  CHECK_EQ(rc, CHRONOS_OK);
  CHECK(error.empty());
  return image;
}

inline void remove_file(const std::string& path) { std::remove(path.c_str()); }

/* Minimum / maximum channel value of an image (used for seam-invariance checks). */
inline void channel_range(const chronos::Image& image, int channel, int* minimum, int* maximum) {
  int low = 255;
  int high = 0;
  const size_t pixels = static_cast<size_t>(image.width) * static_cast<size_t>(image.height);
  for (size_t i = 0; i < pixels; ++i) {
    const int value = image.pixels[i * static_cast<size_t>(image.channels) +
                                   static_cast<size_t>(channel)];
    low = std::min(low, value);
    high = std::max(high, value);
  }
  *minimum = low;
  *maximum = high;
}

/* Maximum absolute difference per pixel/channel between two identically sized images. */
inline int max_abs_diff(const chronos::Image& a, const chronos::Image& b) {
  CHECK_EQ(a.width, b.width);
  CHECK_EQ(a.height, b.height);
  CHECK_EQ(a.channels, b.channels);
  int largest = 0;
  const size_t count = a.pixels.size();
  for (size_t i = 0; i < count; ++i) {
    largest = std::max(largest, std::abs(static_cast<int>(a.pixels[i]) -
                                         static_cast<int>(b.pixels[i])));
  }
  return largest;
}

}  // namespace chronos_test

#endif /* CHRONOS_TEST_UTIL_H */
