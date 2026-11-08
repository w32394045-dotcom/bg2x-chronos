/*
 * Project Chronos — separable 8-bit resampler.
 *
 * Used by the stub backend (Lanczos3 / bilinear), and to scale the alpha plane
 * when CHRONOS_FLAG_PRESERVE_ALPHA is set. Correct prefilter scaling is applied
 * for both up- and down-scaling: the filter support widens by src/dst when
 * shrinking, so downscaled output is properly low-pass filtered instead of
 * point-sampled. Edges are clamped (replicate padding).
 */
#ifndef CHRONOS_RESAMPLE_H
#define CHRONOS_RESAMPLE_H

#include <cstddef>
#include <cstdint>
#include <vector>

namespace chronos {

enum class ResampleFilter { kLanczos3 = 0, kBilinear = 1 };

/* Lanczos3 kernel: sinc(x) * sinc(x / 3), support |x| < 3. */
double lanczos3_kernel(double x) noexcept;

/*
 * Precomputed separable resampler. Construction builds the tap tables once;
 * run() performs two passes (horizontal -> float scratch -> vertical) and is
 * safe to call concurrently from several threads as long as each call gets its
 * own scratch buffer. No allocation happens in run().
 */
class Resampler {
 public:
  Resampler(int src_w, int src_h, int dst_w, int dst_h, int channels, ResampleFilter filter);

  void run(const uint8_t* src, uint8_t* dst, float* scratch) const;

  size_t scratch_floats() const noexcept { return scratch_floats_; }
  int src_width() const noexcept { return src_w_; }
  int src_height() const noexcept { return src_h_; }
  int dst_width() const noexcept { return dst_w_; }
  int dst_height() const noexcept { return dst_h_; }
  int channels() const noexcept { return channels_; }
  bool identity() const noexcept { return identity_; }

 private:
  struct Tap {
    int index;   /* clamped source index */
    float weight;
  };

  static void build_taps(int src_size, int dst_size, ResampleFilter filter,
                         std::vector<Tap>* taps, std::vector<int>* offsets);

  int src_w_ = 0;
  int src_h_ = 0;
  int dst_w_ = 0;
  int dst_h_ = 0;
  int channels_ = 0;
  bool identity_ = false;
  size_t scratch_floats_ = 0;
  std::vector<Tap> h_taps_;
  std::vector<int> h_offsets_; /* dst_w + 1 entries */
  std::vector<Tap> v_taps_;
  std::vector<int> v_offsets_; /* dst_h + 1 entries */
};

/* Convenience wrapper that allocates its own scratch (tests, alpha plane). */
void resample_image(const uint8_t* src, int src_w, int src_h, int channels, uint8_t* dst,
                    int dst_w, int dst_h, ResampleFilter filter);

}  // namespace chronos

#endif /* CHRONOS_RESAMPLE_H */
