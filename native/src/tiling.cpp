/*
 * Project Chronos — tile planner implementation.
 */
#include "chronos/tiling.h"

#include "chronos/chronos.h"
#include "chronos/options.h"
#include "common.h"

namespace chronos {

std::vector<int> axis_positions(int length, int tile_size, int step) {
  std::vector<int> positions;
  if (length <= 0 || tile_size <= 0) return positions;
  if (step < 1) step = 1;

  if (length <= tile_size) {
    /* Single tile, completed by edge replication. */
    positions.push_back(0);
    return positions;
  }

  const int last_origin = length - tile_size; /* >= 0 */
  int origin = 0;
  for (;;) {
    if (origin + tile_size > length) origin = last_origin; /* clamp to stay in bounds */
    positions.push_back(origin);
    if (origin >= last_origin) break;
    origin += step;
  }
  return positions;
}

int plan_tiles(int image_w, int image_h, int tile_size, int overlap, int scale, TilePlan* plan,
               std::string* err) {
  if (plan == nullptr) {
    set_error(err, "plan_tiles: null plan");
    return CHRONOS_ERR_PARAM;
  }
  *plan = TilePlan();
  if (image_w <= 0 || image_h <= 0) {
    set_error(err, "invalid image dimensions");
    return CHRONOS_ERR_PARAM;
  }
  if (tile_size <= 0 || tile_size > kMaxTileSize) {
    set_error(err, "invalid tile_size");
    return CHRONOS_ERR_PARAM;
  }
  if (overlap < 0 || overlap >= tile_size) {
    set_error(err, "overlap must satisfy 0 <= overlap < tile_size");
    return CHRONOS_ERR_PARAM;
  }
  if (scale <= 0) {
    set_error(err, "invalid scale");
    return CHRONOS_ERR_PARAM;
  }

  const int step = tile_size - overlap;
  const std::vector<int> xs = axis_positions(image_w, tile_size, step);
  const std::vector<int> ys = axis_positions(image_h, tile_size, step);
  if (xs.empty() || ys.empty()) {
    set_error(err, "empty tile grid");
    return CHRONOS_ERR_PARAM;
  }

  plan->image_w = image_w;
  plan->image_h = image_h;
  plan->tile_size = tile_size;
  plan->overlap = overlap;
  plan->step = step;
  plan->scale = scale;
  plan->cols = static_cast<int>(xs.size());
  plan->rows = static_cast<int>(ys.size());
  plan->patch_size = tile_size * scale;

  const long long out_w = static_cast<long long>(image_w) * scale;
  const long long out_h = static_cast<long long>(image_h) * scale;
  if (out_w <= 0 || out_h <= 0 || out_w * out_h > kMaxOutputPixels) {
    set_error(err, "output image is too large");
    return CHRONOS_ERR_PARAM;
  }
  plan->out_w = static_cast<int>(out_w);
  plan->out_h = static_cast<int>(out_h);

  plan->tiles.reserve(static_cast<size_t>(plan->cols) * static_cast<size_t>(plan->rows));
  for (int y : ys) {
    for (int x : xs) {
      Tile tile;
      tile.x = x;
      tile.y = y;
      plan->tiles.push_back(tile);
    }
  }
  return CHRONOS_OK;
}

bool tiles_cover_output(const TilePlan& plan) {
  if (plan.empty() || plan.out_w <= 0 || plan.out_h <= 0) return false;
  std::vector<uint8_t> covered(static_cast<size_t>(plan.out_w) * static_cast<size_t>(plan.out_h), 0);
  for (const Tile& tile : plan.tiles) {
    const int left = clamp_value(tile.x * plan.scale, 0, plan.out_w);
    const int top = clamp_value(tile.y * plan.scale, 0, plan.out_h);
    const int right = clamp_value(tile.x * plan.scale + plan.patch_size, 0, plan.out_w);
    const int bottom = clamp_value(tile.y * plan.scale + plan.patch_size, 0, plan.out_h);
    for (int y = top; y < bottom; ++y) {
      std::memset(covered.data() + static_cast<size_t>(y) * plan.out_w + left, 1,
                  static_cast<size_t>(right - left));
    }
  }
  for (uint8_t value : covered) {
    if (value == 0) return false;
  }
  return true;
}

void extract_tile_rgb(const Image& src, int x, int y, int tile_size, uint8_t* dst) {
  const int channels = src.channels;
  const int max_x = src.width - 1;
  const int max_y = src.height - 1;
  for (int j = 0; j < tile_size; ++j) {
    const int sy = clamp_value(y + j, 0, max_y);
    const uint8_t* src_row = src.row(sy);
    uint8_t* dst_row = dst + static_cast<size_t>(j) * static_cast<size_t>(tile_size) * 3;
    for (int i = 0; i < tile_size; ++i) {
      const int sx = clamp_value(x + i, 0, max_x);
      const uint8_t* pixel = src_row + static_cast<size_t>(sx) * static_cast<size_t>(channels);
      if (channels >= 3) {
        dst_row[i * 3 + 0] = pixel[0];
        dst_row[i * 3 + 1] = pixel[1];
        dst_row[i * 3 + 2] = pixel[2];
      } else {
        /* Grayscale (optionally gray+alpha): replicate the single channel. */
        dst_row[i * 3 + 0] = pixel[0];
        dst_row[i * 3 + 1] = pixel[0];
        dst_row[i * 3 + 2] = pixel[0];
      }
    }
  }
}

}  // namespace chronos
