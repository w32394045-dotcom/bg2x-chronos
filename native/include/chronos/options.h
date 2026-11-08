/*
 * Project Chronos — internal C++ option handling.
 *
 * Mirrors the frozen C struct `chronos_options` (native/include/chronos/chronos.h)
 * but adds validated defaults / caps before anything is allocated.
 */
#ifndef CHRONOS_OPTIONS_H
#define CHRONOS_OPTIONS_H

#include <string>

#include <chronos/chronos.h>

namespace chronos {

/* Defaults from docs/CONTRACTS.md section 2. */
constexpr int kDefaultTileSize = 256;
constexpr int kDefaultOverlap = 16;
constexpr int kDefaultStubScale = 4;

/* Hard concurrency cap (docs/CONTRACTS.md section 0, rule 5). */
constexpr int kMaxThreads = 8;

/* Sanity limits: reject absurd requests instead of attempting huge allocations. */
constexpr int kMinTileSize = 8;
constexpr int kMaxTileSize = 8192;
constexpr int kMinStubScale = 1;
constexpr int kMaxStubScale = 4;

/* 256 Mi pixels ~= 4 GB of float accumulation buffers. */
constexpr long long kMaxOutputPixels = 268435456LL;

constexpr int kKnownFlags = CHRONOS_FLAG_FAST | CHRONOS_FLAG_PRESERVE_ALPHA;

/* Validated, defaulted copy of chronos_options. */
struct Options {
  int tile_size = kDefaultTileSize;
  int overlap = kDefaultOverlap;
  int threads = 0;
  int device_index = 0;
  int flags = 0;

  bool fast() const noexcept { return (flags & CHRONOS_FLAG_FAST) != 0; }
  bool preserve_alpha() const noexcept { return (flags & CHRONOS_FLAG_PRESERVE_ALPHA) != 0; }

  /* tile_size - overlap; always >= 1 after normalize_options(). */
  int step() const noexcept { return tile_size - overlap; }

  /* 1..kMaxThreads. */
  int effective_threads() const noexcept;
};

/* std::thread::hardware_concurrency() capped to kMaxThreads, min 1. */
int hardware_threads() noexcept;

/*
 * Applies the documented defaults (tile<=0 => 256, overlap<=0 => 16,
 * threads<=0 => hardware, capped at 8), then validates:
 *   - 8 <= tile_size <= 8192
 *   - 1 <= overlap < tile_size
 *   - device_index >= 0
 *   - only known flag bits set
 * Returns CHRONOS_OK or CHRONOS_ERR_PARAM with a human readable *err.
 */
int normalize_options(const Options& in, Options* out, std::string* err);

}  // namespace chronos

#endif /* CHRONOS_OPTIONS_H */
