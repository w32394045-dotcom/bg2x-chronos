/*
 * Project Chronos — feather blending tests.
 *
 * Covers: raised-linear ramp weights, weight normalisation (no zero weights),
 * constant-sum overlap property, bit-exact constant image (zero variance) and
 * seam-free gradient reconstruction.
 */
#include <chronos/blending.h>
#include <chronos/chronos.h>
#include <chronos/tiling.h>

#include <algorithm>
#include <cstddef>
#include <string>
#include <vector>

#include "harness.h"
#include "test_util.h"

namespace {

/* Accumulated per-axis weight sums for a plan (scale 1 so local == global). */
std::vector<float> axis_weight_sums(const chronos::TilePlan& plan) {
  std::vector<float> sums(static_cast<size_t>(plan.out_w), 0.0f);
  const std::vector<float> weights = chronos::BlendAccumulator::axis_weights(plan.patch_size);
  for (const chronos::Tile& tile : plan.tiles) {
    for (int i = 0; i < plan.patch_size; ++i) {
      const int x = tile.x * plan.scale + i;
      if (x >= 0 && x < plan.out_w) sums[static_cast<size_t>(x)] += weights[static_cast<size_t>(i)];
    }
  }
  return sums;
}

}  // namespace

TEST(blending, axis_weights_are_raised_linear_with_floor) {
  const std::vector<float> weights = chronos::BlendAccumulator::axis_weights(8);
  CHECK_EQ(weights.size(), static_cast<size_t>(8));
  const float expected[8] = {1.0f, 2.0f, 3.0f, 4.0f, 4.0f, 3.0f, 2.0f, 1.0f};
  for (size_t i = 0; i < 8; ++i) CHECK_EQ(weights[i], expected[i]);

  /* Ends are 1 (never zero), the ramp grows then shrinks symmetrically. */
  const std::vector<float> large = chronos::BlendAccumulator::axis_weights(32);
  CHECK_EQ(large.front(), 1.0f);
  CHECK_EQ(large.back(), 1.0f);
  for (size_t i = 0; i < large.size(); ++i) {
    CHECK(large[i] >= 1.0f);
    CHECK_EQ(large[i], large[large.size() - 1 - i]);
  }
  CHECK(chronos::BlendAccumulator::axis_weights(0).empty());
}

TEST(blending, overlapping_weights_sum_to_constant) {
  chronos::TilePlan plan;
  std::string error;
  /* Single tile row: 96 wide, tile 32, overlap 8 -> origins 0, 24, 48, 64. */
  CHECK_EQ(chronos::plan_tiles(96, 32, 32, 8, 1, &plan, &error), CHRONOS_OK);
  CHECK_EQ(plan.rows, 1);
  CHECK_EQ(plan.cols, 4);

  const std::vector<float> sums = axis_weight_sums(plan);
  /* Regular overlap between tile 0 and tile 24: two ramps summing to a constant
   * (overlap * scale + 1 = 9) for output x in [24, 32). */
  for (int x = 24; x < 32; ++x) {
    CHECK_NEAR(sums[static_cast<size_t>(x)], 9.0, 1e-6);
  }
  /* Same property between tile 24 and tile 48 (x in [48, 56)). */
  for (int x = 48; x < 56; ++x) {
    CHECK_NEAR(sums[static_cast<size_t>(x)], 9.0, 1e-6);
  }
  /* The last tile is clamped (spacing 16 instead of 24), so its overlap with
   * tile 48 sums to tile_size - spacing + 1 = 17 there; still a constant. */
  for (int x = 64; x < 80; ++x) {
    CHECK_NEAR(sums[static_cast<size_t>(x)], 17.0, 1e-6);
  }
  /* Every pixel has a strictly positive weight (normalisation is safe). */
  for (float sum : sums) CHECK(sum > 0.0f);
}

TEST(blending, constant_patches_resolve_bit_exact) {
  /* This is the seam-invariance property: a constant input must come out as the
   * exact same constant, with zero variance, no matter how tiles overlap. */
  chronos::TilePlan plan;
  std::string error;
  CHECK_EQ(chronos::plan_tiles(64, 48, 32, 8, 2, &plan, &error), CHRONOS_OK);

  chronos::BlendAccumulator accumulator;
  accumulator.reset(plan.out_w, plan.out_h, 3);
  const std::vector<float> axis = chronos::BlendAccumulator::axis_weights(plan.patch_size);

  std::vector<uint8_t> patch(static_cast<size_t>(plan.patch_size) * plan.patch_size * 3);
  for (size_t i = 0; i < patch.size(); i += 3) {
    patch[i + 0] = 137;
    patch[i + 1] = 63;
    patch[i + 2] = 200;
  }
  for (const chronos::Tile& tile : plan.tiles) {
    accumulator.accumulate(patch.data(), plan.patch_size, plan.patch_size, tile.x * plan.scale,
                           tile.y * plan.scale, axis);
  }
  for (float sum : accumulator.weight_sum()) CHECK(sum > 0.0f);

  chronos::Image output;
  accumulator.resolve(&output);
  CHECK_EQ(output.width, 128);
  CHECK_EQ(output.height, 96);
  CHECK_EQ(output.channels, 3);
  for (size_t i = 0; i < output.pixels.size(); i += 3) {
    CHECK_EQ(output.pixels[i + 0], 137);
    CHECK_EQ(output.pixels[i + 1], 63);
    CHECK_EQ(output.pixels[i + 2], 200);
  }
}

TEST(blending, gradient_survives_weights_bit_exact_scale_one) {
  /* With scale 1 the patches carry the original pixels, so the weighted average
   * of overlapping identical values must reproduce the input exactly. */
  chronos::TilePlan plan;
  std::string error;
  CHECK_EQ(chronos::plan_tiles(96, 32, 32, 8, 1, &plan, &error), CHRONOS_OK);

  chronos::Image source = chronos_test::make_gradient_rgb(plan.image_w, plan.image_h);
  chronos::BlendAccumulator accumulator;
  accumulator.reset(plan.out_w, plan.out_h, 3);
  const std::vector<float> axis = chronos::BlendAccumulator::axis_weights(plan.patch_size);

  std::vector<uint8_t> patch(static_cast<size_t>(plan.patch_size) * plan.patch_size * 3);
  for (const chronos::Tile& tile : plan.tiles) {
    for (int j = 0; j < plan.tile_size; ++j) {
      const int source_y = std::min(plan.image_h - 1, tile.y + j);
      for (int i = 0; i < plan.tile_size; ++i) {
        const int source_x = std::min(plan.image_w - 1, tile.x + i);
        const uint8_t* pixel = source.row(source_y) + static_cast<size_t>(source_x) * 3;
        uint8_t* destination =
            patch.data() + (static_cast<size_t>(j) * plan.tile_size + i) * 3;
        destination[0] = pixel[0];
        destination[1] = pixel[1];
        destination[2] = pixel[2];
      }
    }
    accumulator.accumulate(patch.data(), plan.patch_size, plan.patch_size, tile.x * plan.scale,
                           tile.y * plan.scale, axis);
  }

  chronos::Image output;
  accumulator.resolve(&output);
  CHECK_EQ(chronos_test::max_abs_diff(output, source), 0);
}

TEST(blending, resolve_handles_single_patch) {
  chronos::BlendAccumulator accumulator;
  accumulator.reset(16, 16, 3);
  const std::vector<float> axis = chronos::BlendAccumulator::axis_weights(16);
  std::vector<uint8_t> patch(16 * 16 * 3, 77);
  accumulator.accumulate(patch.data(), 16, 16, 0, 0, axis);
  chronos::Image output;
  accumulator.resolve(&output);
  CHECK_EQ(output.width, 16);
  CHECK_EQ(output.height, 16);
  CHECK_EQ(output.channels, 3);
  for (size_t i = 0; i < output.pixels.size(); i += 3) {
    CHECK_EQ(output.pixels[i + 0], 77);
    CHECK_EQ(output.pixels[i + 1], 77);
    CHECK_EQ(output.pixels[i + 2], 77);
  }
}

TEST(blending, clips_patches_outside_the_canvas) {
  chronos::BlendAccumulator accumulator;
  accumulator.reset(8, 8, 3);
  const std::vector<float> axis = chronos::BlendAccumulator::axis_weights(8);
  std::vector<uint8_t> patch(8 * 8 * 3, 50);
  /* Negative origin: only the overlapping half may be written. */
  accumulator.accumulate(patch.data(), 8, 8, -4, -4, axis);
  chronos::Image output;
  accumulator.resolve(&output);
  for (int y = 0; y < 8; ++y) {
    for (int x = 0; x < 8; ++x) {
      const bool inside = x < 4 && y < 4;
      CHECK_EQ(output.row(y)[static_cast<size_t>(x) * 3], inside ? 50 : 0);
    }
  }
}
