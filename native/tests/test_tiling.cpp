/*
 * Project Chronos — tile planner tests.
 *
 * Covers: axis positions, 2D grid, coverage without gaps, edge clamping,
 * arbitrary tile/overlap combinations, invalid parameters, replicate padding.
 */
#include <chronos/tiling.h>

#include <algorithm>
#include <cstddef>

#include "harness.h"

namespace {

struct GridCase {
  int width;
  int height;
  int tile;
  int overlap;
  int scale;
};

}  // namespace

TEST(tiling, axis_positions_default_geometry) {
  /* 64 pixels, tile 32, step 24 -> origins 0, 24 and the clamped 32. */
  const std::vector<int> positions = chronos::axis_positions(64, 32, 24);
  CHECK_EQ(positions.size(), static_cast<size_t>(3));
  CHECK_EQ(positions[0], 0);
  CHECK_EQ(positions[1], 24);
  CHECK_EQ(positions[2], 32);

  /* Exactly one tile when the axis is not longer than a tile. */
  CHECK_EQ(chronos::axis_positions(32, 32, 24).size(), static_cast<size_t>(1));
  CHECK_EQ(chronos::axis_positions(10, 32, 24).size(), static_cast<size_t>(1));
}

TEST(tiling, two_dimensional_plan_for_pipeline_case) {
  chronos::TilePlan plan;
  std::string error;
  /* The documented pipeline test case: 64x48 -> 128x96, tile 32, overlap 8, scale 2. */
  CHECK_EQ(chronos::plan_tiles(64, 48, 32, 8, 2, &plan, &error), CHRONOS_OK);
  CHECK(error.empty());
  CHECK_EQ(plan.cols, 3);
  CHECK_EQ(plan.rows, 2);
  CHECK_EQ(plan.count(), 6);
  CHECK_EQ(plan.step, 24);
  CHECK_EQ(plan.patch_size, 64);
  CHECK_EQ(plan.out_w, 128);
  CHECK_EQ(plan.out_h, 96);
  CHECK(chronos::tiles_cover_output(plan));

  for (const chronos::Tile& tile : plan.tiles) {
    CHECK(tile.x >= 0);
    CHECK(tile.y >= 0);
    CHECK(tile.x + plan.tile_size <= plan.image_w);
    CHECK(tile.y + plan.tile_size <= plan.image_h);
  }
}

TEST(tiling, coverage_and_clamping_for_many_geometries) {
  const GridCase cases[] = {
      {64, 48, 32, 8, 2},    {100, 100, 16, 4, 3},  {10, 7, 32, 8, 2},
      {33, 17, 8, 2, 1},     {256, 256, 256, 16, 4}, {500, 300, 64, 16, 2},
      {9, 9, 8, 1, 1},       {17, 300, 16, 3, 2},   {300, 17, 16, 3, 2},
      {1, 1, 8, 1, 1},
  };

  for (const GridCase& test_case : cases) {
    chronos::TilePlan plan;
    std::string error;
    CHECK_EQ(chronos::plan_tiles(test_case.width, test_case.height, test_case.tile,
                                 test_case.overlap, test_case.scale, &plan, &error),
             CHRONOS_OK);
    CHECK(error.empty());
    CHECK_EQ(plan.count(), plan.cols * plan.rows);
    CHECK(chronos::tiles_cover_output(plan));

    /* Origin clamps: never negative, never lets the tile leave the image. */
    for (const chronos::Tile& tile : plan.tiles) {
      CHECK(tile.x >= 0);
      CHECK(tile.y >= 0);
      CHECK(tile.x <= plan.image_w - std::min(plan.tile_size, plan.image_w));
      CHECK(tile.y <= plan.image_h - std::min(plan.tile_size, plan.image_h));
      if (plan.image_w >= plan.tile_size) CHECK(tile.x + plan.tile_size <= plan.image_w);
      if (plan.image_h >= plan.tile_size) CHECK(tile.y + plan.tile_size <= plan.image_h);
    }

    /* Neighbouring origins are never further apart than tile - overlap (no gaps). */
    for (int row = 0; row < plan.rows; ++row) {
      for (int col = 1; col < plan.cols; ++col) {
        const chronos::Tile& previous = plan.tiles[static_cast<size_t>(row * plan.cols + col - 1)];
        const chronos::Tile& current = plan.tiles[static_cast<size_t>(row * plan.cols + col)];
        CHECK_EQ(current.y, previous.y);
        CHECK(current.x - previous.x <= plan.step);
        CHECK(current.x > previous.x);
      }
    }
  }
}

TEST(tiling, plan_guarantees_output_coverage_per_tile_patch) {
  /* Independently verify the coverage helper against a brute-force union. */
  chronos::TilePlan plan;
  std::string error;
  CHECK_EQ(chronos::plan_tiles(96, 40, 32, 8, 2, &plan, &error), CHRONOS_OK);

  std::vector<int> hits(static_cast<size_t>(plan.out_w) * static_cast<size_t>(plan.out_h), 0);
  for (const chronos::Tile& tile : plan.tiles) {
    const int left = tile.x * plan.scale;
    const int top = tile.y * plan.scale;
    const int right = std::min(plan.out_w, left + plan.patch_size);
    const int bottom = std::min(plan.out_h, top + plan.patch_size);
    CHECK(left >= 0);
    CHECK(top >= 0);
    CHECK(right > left);
    CHECK(bottom > top);
    for (int y = top; y < bottom; ++y) {
      for (int x = left; x < right; ++x) {
        ++hits[static_cast<size_t>(y) * static_cast<size_t>(plan.out_w) + static_cast<size_t>(x)];
      }
    }
  }
  for (int count : hits) CHECK(count >= 1);
}

TEST(tiling, rejects_invalid_parameters) {
  chronos::TilePlan plan;
  std::string error;
  CHECK_EQ(chronos::plan_tiles(0, 10, 16, 4, 2, &plan, &error), CHRONOS_ERR_PARAM);
  CHECK(!error.empty());
  CHECK_EQ(chronos::plan_tiles(10, 0, 16, 4, 2, &plan, &error), CHRONOS_ERR_PARAM);
  CHECK_EQ(chronos::plan_tiles(10, 10, 0, 4, 2, &plan, &error), CHRONOS_ERR_PARAM);
  CHECK_EQ(chronos::plan_tiles(10, 10, 16, 16, 2, &plan, &error), CHRONOS_ERR_PARAM);
  CHECK_EQ(chronos::plan_tiles(10, 10, 16, -1, 2, &plan, &error), CHRONOS_ERR_PARAM);
  CHECK_EQ(chronos::plan_tiles(10, 10, 16, 4, 0, &plan, &error), CHRONOS_ERR_PARAM);
  CHECK_EQ(chronos::plan_tiles(10, 10, 16, 4, 2, nullptr, &error), CHRONOS_ERR_PARAM);
  CHECK(!chronos::tiles_cover_output(plan));
}

TEST(tiling, extract_tile_replicates_edges) {
  /* 3x2 image, 4x4 tile located at (0,0): everything outside is clamped. */
  chronos::Image source;
  source.allocate(3, 2, 3);
  for (int y = 0; y < source.height; ++y) {
    for (int x = 0; x < source.width; ++x) {
      uint8_t* pixel = source.row(y) + static_cast<size_t>(x) * 3;
      pixel[0] = static_cast<uint8_t>(x);
      pixel[1] = static_cast<uint8_t>(y);
      pixel[2] = 7;
    }
  }
  std::vector<uint8_t> tile(4 * 4 * 3);
  chronos::extract_tile_rgb(source, 0, 0, 4, tile.data());
  auto at = [&tile](int x, int y, int channel) -> int {
    return tile[(static_cast<size_t>(y) * 4 + static_cast<size_t>(x)) * 3 +
                static_cast<size_t>(channel)];
  };
  CHECK_EQ(at(0, 0, 0), 0); /* x=0 */
  CHECK_EQ(at(2, 0, 0), 2); /* last real column */
  CHECK_EQ(at(3, 0, 0), 2); /* replicated edge */
  CHECK_EQ(at(0, 1, 1), 1); /* last real row */
  CHECK_EQ(at(0, 2, 1), 1); /* replicated edge */
  CHECK_EQ(at(0, 3, 1), 1);
  CHECK_EQ(at(3, 3, 1), 1);
  CHECK_EQ(at(1, 1, 2), 7);
}

TEST(tiling, extract_tile_from_rgba_source_uses_rgb_only) {
  chronos::Image source;
  source.allocate(2, 2, 4);
  for (size_t i = 0; i < 4; ++i) {
    source.pixels[i * 4 + 0] = 10;
    source.pixels[i * 4 + 1] = 20;
    source.pixels[i * 4 + 2] = 30;
    source.pixels[i * 4 + 3] = 40;
  }
  std::vector<uint8_t> tile(8 * 8 * 3);
  chronos::extract_tile_rgb(source, 0, 0, 8, tile.data());
  for (int i = 0; i < 8 * 8; ++i) {
    CHECK_EQ(tile[static_cast<size_t>(i) * 3 + 0], 10);
    CHECK_EQ(tile[static_cast<size_t>(i) * 3 + 1], 20);
    CHECK_EQ(tile[static_cast<size_t>(i) * 3 + 2], 30);
  }
}
