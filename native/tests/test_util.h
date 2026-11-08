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
#include <filesystem>
#include <string>
#include <system_error>
#include <vector>

#if defined(_WIN32)
#include <process.h>
#else
#include <unistd.h>
#endif

#include "harness.h"

namespace chronos_test {

/*
 * Scratch directory for every test file.
 *
 * ctest exports CHRONOS_TEST_TMPDIR (a directory outside the working tree) so
 * the CLI hand-off test can pass one PNG between two processes. A manually run
 * test binary falls back to a per-process directory below the system temp dir,
 * so a test can never drop artifacts into the repository working tree.
 */
inline std::string temp_directory() {
  static const std::string directory = []() {
    std::string directory_path;
    if (const char* environment = std::getenv("CHRONOS_TEST_TMPDIR");
        environment != nullptr && *environment != '\0') {
      directory_path = environment;
    } else {
      std::error_code error;
      const std::filesystem::path system_temp = std::filesystem::temp_directory_path(error);
      const std::string base = error ? std::string(".") : system_temp.string();
#if defined(_WIN32)
      const long process_id = static_cast<long>(::_getpid());
#else
      const long process_id = static_cast<long>(::getpid());
#endif
      directory_path = base + "/chronos_native_tests/run_" + std::to_string(process_id);
    }
    std::error_code create_error;
    std::filesystem::create_directories(directory_path, create_error);
    return directory_path;
  }();
  return directory;
}

/* Absolute path of a test file; see temp_directory(). */
inline std::string temp_path(const std::string& name) { return temp_directory() + "/" + name; }

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
