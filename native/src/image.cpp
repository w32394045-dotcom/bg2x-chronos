/*
 * Project Chronos — image container and file IO (stb_image / stb_image_write).
 */
#include "chronos/image.h"

#include <cctype>
#include <cstring>
#include <new>
#include <stdexcept>

#include "chronos/chronos.h"
#include "common.h"

/* Declarations only; the implementation lives in stb_impl.cpp. */
#include "stb_image.h"
#include "stb_image_write.h"

namespace chronos {
namespace {

constexpr int kJpegQuality = 95;

std::string to_lower_ascii(const std::string& value) {
  std::string lowered;
  lowered.reserve(value.size());
  for (char c : value) {
    lowered.push_back(static_cast<char>(std::tolower(static_cast<unsigned char>(c))));
  }
  return lowered;
}

}  // namespace

void Image::allocate(int w, int h, int c) {
  if (w <= 0 || h <= 0 || c < 1 || c > 4) {
    throw std::invalid_argument("Image::allocate: invalid dimensions");
  }
  width = w;
  height = h;
  channels = c;
  pixels.assign(byte_size(), 0);
}

void Image::fill(uint8_t value) noexcept {
  if (!pixels.empty()) std::memset(pixels.data(), value, pixels.size());
}

ImageFormat format_from_extension(const std::string& path) {
  const size_t slash = path.find_last_of("/\\");
  const size_t dot = path.find_last_of('.');
  if (dot == std::string::npos) return ImageFormat::kUnknown;
  if (slash != std::string::npos && dot < slash) return ImageFormat::kUnknown;
  const std::string ext = to_lower_ascii(path.substr(dot + 1));
  if (ext == "png") return ImageFormat::kPng;
  if (ext == "jpg" || ext == "jpeg") return ImageFormat::kJpeg;
  if (ext == "bmp") return ImageFormat::kBmp;
  return ImageFormat::kUnknown;
}

int load_image(const std::string& path, bool preserve_alpha, Image* out, std::string* err) {
  if (out == nullptr) {
    set_error(err, "load_image: null output image");
    return CHRONOS_ERR_PARAM;
  }
  *out = Image();
  if (path.empty()) {
    set_error(err, "load_image: empty input path");
    return CHRONOS_ERR_PARAM;
  }

  /* stb down-converts every format it understands (palette, 16-bit, gray,
   * gray+alpha, ...) to 8-bit with the requested channel count. */
  const int desired_channels = preserve_alpha ? 4 : 3;
  int width = 0;
  int height = 0;
  int source_channels = 0;
  unsigned char* data =
      stbi_load(path.c_str(), &width, &height, &source_channels, desired_channels);
  if (data == nullptr) {
    const char* reason = stbi_failure_reason();
    std::string message = "cannot load image: " + path;
    if (reason != nullptr && *reason != '\0') message += " (";
    if (reason != nullptr) message += reason;
    if (reason != nullptr && *reason != '\0') message += ")";
    set_error(err, message);
    return CHRONOS_ERR_IO;
  }
  if (width <= 0 || height <= 0) {
    stbi_image_free(data);
    set_error(err, "cannot load image: invalid dimensions in " + path);
    return CHRONOS_ERR_IO;
  }

  try {
    out->allocate(width, height, desired_channels);
  } catch (...) {
    stbi_image_free(data);
    set_error(err, "out of memory while loading image: " + path);
    return CHRONOS_ERR_IO;
  }
  std::memcpy(out->pixels.data(), data, out->byte_size());
  stbi_image_free(data);
  return CHRONOS_OK;
}

int save_image(const std::string& path, const Image& image, std::string* err) {
  if (path.empty()) {
    set_error(err, "save_image: empty output path");
    return CHRONOS_ERR_PARAM;
  }
  if (image.empty() || image.channels < 1 || image.channels > 4) {
    set_error(err, "save_image: invalid image");
    return CHRONOS_ERR_PARAM;
  }
  const ImageFormat format = format_from_extension(path);
  if (format == ImageFormat::kUnknown) {
    set_error(err, "unsupported output extension (use .png, .jpg, .jpeg or .bmp): " + path);
    return CHRONOS_ERR_IO;
  }

  const int width = image.width;
  const int height = image.height;
  const int channels = image.channels;
  const int stride = static_cast<int>(image.stride());
  int ok = 0;

  if (format == ImageFormat::kPng) {
    ok = stbi_write_png(path.c_str(), width, height, channels, image.pixels.data(), stride);
  } else if (format == ImageFormat::kBmp) {
    ok = stbi_write_bmp(path.c_str(), width, height, channels, image.pixels.data());
  } else {
    /* JPEG has no alpha channel: drop it explicitly. */
    if (channels == 4) {
      std::vector<uint8_t> rgb(static_cast<size_t>(width) * static_cast<size_t>(height) * 3);
      for (size_t i = 0, src = 0; i < rgb.size(); i += 3, src += 4) {
        rgb[i + 0] = image.pixels[src + 0];
        rgb[i + 1] = image.pixels[src + 1];
        rgb[i + 2] = image.pixels[src + 2];
      }
      ok = stbi_write_jpg(path.c_str(), width, height, 3, rgb.data(), kJpegQuality);
    } else if (channels == 2) {
      std::vector<uint8_t> gray(static_cast<size_t>(width) * static_cast<size_t>(height));
      for (size_t i = 0, src = 0; i < gray.size(); ++i, src += 2) gray[i] = image.pixels[src];
      ok = stbi_write_jpg(path.c_str(), width, height, 1, gray.data(), kJpegQuality);
    } else {
      ok = stbi_write_jpg(path.c_str(), width, height, channels, image.pixels.data(),
                          kJpegQuality);
    }
  }

  if (ok == 0) {
    set_error(err, "cannot write image (check that the directory exists): " + path);
    return CHRONOS_ERR_IO;
  }
  return CHRONOS_OK;
}

}  // namespace chronos
