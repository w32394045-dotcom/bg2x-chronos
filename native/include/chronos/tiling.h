/*
 * Project Chronos — tile planner.
 *
 * Contract (docs/CONTRACTS.md section 2):
 *   - tile = 256, overlap = 16, step = tile - overlap = 240 by default
 *   - output canvas = (image_w * scale, image_h * scale)
 *   - every tile reads a full tile_size x tile_size window from the input,
 *     replicating edge pixels outside the image (clamped padding)
 *   - the last row/column of tiles is clamped so it stays inside the image;
 *     images smaller than one tile produce a single (mostly padded) tile
 *   - every output pixel is covered by at least one tile
 */
#ifndef CHRONOS_TILING_H
#define CHRONOS_TILING_H

#include <string>
#include <vector>

#include "chronos/image.h"

namespace chronos {

struct Tile {
  int x = 0; /* top-left in input pixel coordinates, always >= 0 */
  int y = 0;
};

struct TilePlan {
  int image_w = 0;
  int image_h = 0;
  int tile_size = 0;
  int overlap = 0;
  int step = 0;
  int scale = 1;
  int cols = 0;
  int rows = 0;
  int patch_size = 0; /* tile_size * scale: size of one upscaled patch */
  int out_w = 0;
  int out_h = 0;
  std::vector<Tile> tiles; /* row-major: y-major, x-minor */

  int count() const noexcept { return static_cast<int>(tiles.size()); }
  bool empty() const noexcept { return tiles.empty(); }
};

/*
 * Tile origins along one axis. Always starts at 0; the final origin is clamped
 * to length - tile_size (when length > tile_size) so the last tile stays inside
 * the image; when length <= tile_size a single origin {0} is produced and the
 * tile is completed by edge replication.
 */
std::vector<int> axis_positions(int length, int tile_size, int step);

/*
 * Builds the full tile plan. Returns CHRONOS_OK or CHRONOS_ERR_PARAM with *err.
 * Validates: image dims > 0, tile_size > 0, 0 <= overlap < tile_size, scale > 0,
 * and that out_w * out_h does not exceed kMaxOutputPixels (declared in options.h).
 */
int plan_tiles(int image_w, int image_h, int tile_size, int overlap, int scale,
               TilePlan* plan, std::string* err);

/*
 * Independent coverage check used by tests and by a debug assertion: every
 * output pixel belongs to at least one tile patch (clipped to the canvas).
 * O(out_w * out_h) memory; not used in the hot path.
 */
bool tiles_cover_output(const TilePlan& plan);

/*
 * Copies a tile_size x tile_size RGB window starting at (x, y) from `src` into
 * `dst` (tile_size * tile_size * 3 bytes), replicating edge pixels. `src` may
 * have 1..4 channels; only RGB is extracted.
 */
void extract_tile_rgb(const Image& src, int x, int y, int tile_size, uint8_t* dst);

}  // namespace chronos

#endif /* CHRONOS_TILING_H */
