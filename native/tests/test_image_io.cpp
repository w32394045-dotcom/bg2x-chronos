/*
 * Project Chronos — image IO tests (stb_image / stb_image_write).
 *
 * Covers: PNG/BMP/JPEG roundtrips, RGB->RGBA and RGBA->RGB conversion, gray
 * down-conversion, format detection by extension, UTF-8 (Chinese) paths and the
 * IO error paths.
 */
#include <chronos/chronos.h>
#include <chronos/image.h>

#include <cstddef>
#include <cstdio>
#include <fstream>
#include <string>
#include <vector>

#include "harness.h"
#include "test_util.h"

TEST(image_io, png_roundtrip_rgb_is_lossless) {
  const chronos::Image source = chronos_test::make_gradient_rgb(13, 7);
  const std::string path = chronos_test::temp_path("roundtrip_rgb.png");
  chronos_test::write_image_or_fail(path, source);

  const chronos::Image loaded = chronos_test::read_image_or_fail(path, false);
  CHECK_EQ(loaded.width, 13);
  CHECK_EQ(loaded.height, 7);
  CHECK_EQ(loaded.channels, 3);
  CHECK(loaded.pixels == source.pixels);
  chronos_test::remove_file(path);
}

TEST(image_io, png_roundtrip_rgba_preserves_alpha) {
  const chronos::Image source = chronos_test::make_gradient_rgba(11, 5);
  const std::string path = chronos_test::temp_path("roundtrip_rgba.png");
  chronos_test::write_image_or_fail(path, source);

  const chronos::Image loaded = chronos_test::read_image_or_fail(path, true);
  CHECK_EQ(loaded.channels, 4);
  CHECK_EQ(loaded.width, 11);
  CHECK_EQ(loaded.height, 5);
  CHECK(loaded.pixels == source.pixels);

  /* Loading an RGBA PNG as RGB drops alpha (and keeps the color channels). */
  const chronos::Image rgb = chronos_test::read_image_or_fail(path, false);
  CHECK_EQ(rgb.channels, 3);
  for (size_t i = 0; i < static_cast<size_t>(11) * 5; ++i) {
    CHECK_EQ(rgb.pixels[i * 3 + 0], source.pixels[i * 4 + 0]);
    CHECK_EQ(rgb.pixels[i * 3 + 1], source.pixels[i * 4 + 1]);
    CHECK_EQ(rgb.pixels[i * 3 + 2], source.pixels[i * 4 + 2]);
  }
  chronos_test::remove_file(path);
}

TEST(image_io, rgb_source_loaded_as_rgba_fills_opaque_alpha) {
  const chronos::Image source = chronos_test::make_gradient_rgb(9, 4);
  const std::string path = chronos_test::temp_path("opaque_alpha.png");
  chronos_test::write_image_or_fail(path, source);

  const chronos::Image rgba = chronos_test::read_image_or_fail(path, true);
  CHECK_EQ(rgba.channels, 4);
  for (size_t i = 0; i < static_cast<size_t>(9) * 4; ++i) {
    CHECK_EQ(rgba.pixels[i * 4 + 0], source.pixels[i * 3 + 0]);
    CHECK_EQ(rgba.pixels[i * 4 + 1], source.pixels[i * 3 + 1]);
    CHECK_EQ(rgba.pixels[i * 4 + 2], source.pixels[i * 3 + 2]);
    CHECK_EQ(rgba.pixels[i * 4 + 3], 255);
  }
  chronos_test::remove_file(path);
}

TEST(image_io, grayscale_is_downconverted_to_rgb) {
  chronos::Image gray;
  gray.allocate(7, 3, 1);
  for (int y = 0; y < gray.height; ++y) {
    for (int x = 0; x < gray.width; ++x) gray.row(y)[x] = static_cast<uint8_t>(x * 20);
  }
  const std::string path = chronos_test::temp_path("gray.png");
  chronos_test::write_image_or_fail(path, gray);

  const chronos::Image loaded = chronos_test::read_image_or_fail(path, false);
  CHECK_EQ(loaded.channels, 3);
  for (int y = 0; y < loaded.height; ++y) {
    for (int x = 0; x < loaded.width; ++x) {
      const uint8_t* pixel = loaded.row(y) + static_cast<size_t>(x) * 3;
      CHECK_EQ(pixel[0], gray.row(y)[x]);
      CHECK_EQ(pixel[1], gray.row(y)[x]);
      CHECK_EQ(pixel[2], gray.row(y)[x]);
    }
  }
  chronos_test::remove_file(path);
}

TEST(image_io, bmp_roundtrip_is_lossless) {
  const chronos::Image source = chronos_test::make_gradient_rgb(10, 6);
  const std::string path = chronos_test::temp_path("roundtrip.bmp");
  chronos_test::write_image_or_fail(path, source);
  const chronos::Image loaded = chronos_test::read_image_or_fail(path, false);
  CHECK_EQ(loaded.width, source.width);
  CHECK_EQ(loaded.height, source.height);
  CHECK_EQ(loaded.channels, 3);
  CHECK(loaded.pixels == source.pixels);
  chronos_test::remove_file(path);
}

TEST(image_io, jpeg_roundtrip_keeps_dimensions_and_approximate_content) {
  const chronos::Image source = chronos_test::make_gradient_rgb(32, 24);
  const std::string path = chronos_test::temp_path("roundtrip.jpg");
  chronos_test::write_image_or_fail(path, source);
  const chronos::Image loaded = chronos_test::read_image_or_fail(path, false);
  CHECK_EQ(loaded.width, 32);
  CHECK_EQ(loaded.height, 24);
  CHECK_EQ(loaded.channels, 3);
  /* JPEG is lossy: allow a modest per-channel difference, but not garbage. */
  CHECK(chronos_test::max_abs_diff(loaded, source) <= 40);
  chronos_test::remove_file(path);

  /* RGBA must not break JPEG output (alpha is dropped). */
  const chronos::Image rgba = chronos_test::make_gradient_rgba(16, 16);
  const std::string rgba_path = chronos_test::temp_path("rgba.jpg");
  chronos_test::write_image_or_fail(rgba_path, rgba);
  const chronos::Image jpeg_rgba = chronos_test::read_image_or_fail(rgba_path, false);
  CHECK_EQ(jpeg_rgba.channels, 3);
  CHECK_EQ(jpeg_rgba.width, 16);
  chronos_test::remove_file(rgba_path);
}

TEST(image_io, format_detection_by_extension) {
  CHECK(chronos::format_from_extension("a.png") == chronos::ImageFormat::kPng);
  CHECK(chronos::format_from_extension("a.PNG") == chronos::ImageFormat::kPng);
  CHECK(chronos::format_from_extension("dir/b.JpEg") == chronos::ImageFormat::kJpeg);
  CHECK(chronos::format_from_extension("c.bmp") == chronos::ImageFormat::kBmp);
  CHECK(chronos::format_from_extension("d.txt") == chronos::ImageFormat::kUnknown);
  CHECK(chronos::format_from_extension("no_extension") == chronos::ImageFormat::kUnknown);
  CHECK(chronos::format_from_extension("weird.png/thing") == chronos::ImageFormat::kUnknown);
}

TEST(image_io, unicode_paths_roundtrip) {
  /* The Go layer passes UTF-8 paths; Chinese folder/file names are common. */
  const chronos::Image source = chronos_test::make_constant_rgb(4, 3, 12, 34, 56);
  const std::string path =
      chronos_test::temp_path(u8"测试_图像_chronos.png");
  chronos_test::write_image_or_fail(path, source);
  const chronos::Image loaded = chronos_test::read_image_or_fail(path, false);
  CHECK_EQ(loaded.width, 4);
  CHECK_EQ(loaded.height, 3);
  CHECK(loaded.pixels == source.pixels);
  chronos_test::remove_file(path);
}

TEST(image_io, io_error_paths) {
  chronos::Image image;
  std::string error;

  /* Missing file. */
  CHECK_EQ(chronos::load_image(chronos_test::temp_path("does_not_exist.png"), false, &image,
                               &error),
           CHRONOS_ERR_IO);
  CHECK(!error.empty());

  /* Unsupported output extension. */
  error.clear();
  CHECK_EQ(chronos::save_image(chronos_test::temp_path("out.txt"),
                               chronos_test::make_constant_rgb(2, 2, 1, 2, 3), &error),
           CHRONOS_ERR_IO);
  CHECK(!error.empty());

  /* Corrupt payload masquerading as PNG. */
  const std::string garbage_path = chronos_test::temp_path("garbage.png");
  {
    std::ofstream stream(garbage_path, std::ios::binary);
    stream << "this is definitely not a PNG file";
  }
  error.clear();
  CHECK_EQ(chronos::load_image(garbage_path, false, &image, &error), CHRONOS_ERR_IO);
  CHECK(!error.empty());
  chronos_test::remove_file(garbage_path);

  /* Writing into a directory that does not exist. */
  error.clear();
  CHECK_EQ(chronos::save_image("chronos_missing_dir/out.png",
                               chronos_test::make_constant_rgb(2, 2, 1, 2, 3), &error),
           CHRONOS_ERR_IO);
  CHECK(!error.empty());

  /* Null / empty arguments. */
  error.clear();
  CHECK_EQ(chronos::load_image("", false, &image, &error), CHRONOS_ERR_PARAM);
  CHECK_EQ(chronos::load_image(chronos_test::temp_path("x.png"), false, nullptr, &error),
           CHRONOS_ERR_PARAM);
  CHECK_EQ(chronos::save_image("", chronos_test::make_constant_rgb(2, 2, 1, 2, 3), &error),
           CHRONOS_ERR_PARAM);
}
