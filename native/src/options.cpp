/*
 * Project Chronos — option normalisation / validation.
 */
#include <thread>

#include "chronos/options.h"
#include "common.h"

namespace chronos {

int hardware_threads() noexcept {
  const unsigned int count = std::thread::hardware_concurrency();
  if (count == 0) return 1;
  if (count > static_cast<unsigned int>(kMaxThreads)) return kMaxThreads;
  return static_cast<int>(count);
}

int Options::effective_threads() const noexcept {
  if (threads <= 0) return hardware_threads();
  if (threads > kMaxThreads) return kMaxThreads;
  return threads;
}

int normalize_options(const Options& in, Options* out, std::string* err) {
  if (out == nullptr) {
    set_error(err, "normalize_options: null output");
    return CHRONOS_ERR_PARAM;
  }
  Options options = in;

  /* Documented defaults: tile <= 0 => 256, overlap <= 0 => 16, threads <= 0 => hw. */
  if (options.tile_size <= 0) options.tile_size = kDefaultTileSize;
  if (options.overlap <= 0) options.overlap = kDefaultOverlap;

  if (options.tile_size < kMinTileSize || options.tile_size > kMaxTileSize) {
    set_error(err, "tile_size must be in [8, 8192]");
    return CHRONOS_ERR_PARAM;
  }
  if (options.overlap < 1 || options.overlap >= options.tile_size) {
    set_error(err, "overlap must satisfy 1 <= overlap < tile_size");
    return CHRONOS_ERR_PARAM;
  }
  if (options.device_index < 0) {
    set_error(err, "device_index must be >= 0");
    return CHRONOS_ERR_PARAM;
  }
  if ((options.flags & ~kKnownFlags) != 0) {
    set_error(err, "unknown flag bits in chronos_options.flags");
    return CHRONOS_ERR_PARAM;
  }

  /* Threads: <= 0 means hardware concurrency; the hard cap is always applied. */
  options.threads = options.effective_threads();

  *out = options;
  return CHRONOS_OK;
}

}  // namespace chronos
