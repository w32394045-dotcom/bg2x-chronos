/*
 * Project Chronos — 8-bit interleaved image container + file IO.
 *
 * Image decoding/encoding is done exclusively with the vendored stb headers
 * (native/third_party/stb_image.h, stb_image_write.h). No OpenCV, ever
 * (docs/CONTRACTS.md ADR-001).
 */
#ifndef CHRONOS_IMAGE_H
#define CHRONOS_IMAGE_H

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

namespace chronos {

/* Interleaved 8-bit image. Channels are 1..4, in R,G,B,A order. */
struct Image {
  int width = 0;
  int height = 0;
  int channels = 0;
  std::vector<uint8_t> pixels;

  bool empty() const noexcept { return width <= 0 || height <= 0 || channels <= 0; }
  size_t stride() const noexcept {
    return static_cast<size_t>(width) * static_cast<size_t>(channels);
  }
  size_t byte_size() const noexcept { return stride() * static_cast<size_t>(height); }

  uint8_t* row(int y) noexcept {
    return pixels.data() + static_cast<size_t>(y) * stride();
  }
  const uint8_t* row(int y) const noexcept {
    return pixels.data() + static_cast<size_t>(y) * stride();
  }

  /* Throws std::invalid_argument / std::bad_alloc on impossible requests. */
  void allocate(int w, int h, int c);
  void fill(uint8_t value) noexcept;
};

enum class ImageFormat { kUnknown = 0, kPng, kJpeg, kBmp };

/* Extension driven (case insensitive): .png / .jpg / .jpeg / .bmp. */
ImageFormat format_from_extension(const std::string& path);

/*
 * Decodes any format stb_image understands and down-converts it to 8-bit:
 *   preserve_alpha == false -> exactly 3 channels (RGB)
 *   preserve_alpha == true  -> exactly 4 channels (RGBA, alpha=255 when absent)
 * Returns CHRONOS_OK or CHRONOS_ERR_IO with *err set.
 */
int load_image(const std::string& path, bool preserve_alpha, Image* out, std::string* err);

/*
 * Writes based on the output extension:
 *   .png -> PNG (comp 1..4)
 *   .jpg/.jpeg -> JPEG quality 95 (alpha dropped)
 *   .bmp -> BMP
 * Any other extension is CHRONOS_ERR_IO (we never silently change the format).
 */
int save_image(const std::string& path, const Image& image, std::string* err);

}  // namespace chronos

#endif /* CHRONOS_IMAGE_H */
