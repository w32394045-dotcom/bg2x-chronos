/*
 * Project Chronos — pipeline tests (C++ API).
 *
 * Covers: the documented 64x48 -> 128x96 tiling case, constant image seam
 * invariance, scale-1 identity, fast (bilinear) mode, alpha preservation,
 * session reuse, progress bookkeeping, cancellation and option validation.
 */
#include <chronos/chronos.h>
#include <chronos/image.h>
#include <chronos/options.h>
#include <chronos/pipeline.h>
#include <chronos/tiling.h>

#include <cstddef>
#include <memory>
#include <string>

#include "harness.h"
#include "test_util.h"

namespace {

chronos::Options make_options(int tile, int overlap, int threads, int flags) {
  chronos::Options options;
  options.tile_size = tile;
  options.overlap = overlap;
  options.threads = threads;
  options.flags = flags;
  return options;
}

std::unique_ptr<chronos::Pipeline> create_or_fail(const chronos::Options& options, int scale,
                                                  std::string* error) {
  std::unique_ptr<chronos::Pipeline> pipeline =
      chronos::Pipeline::create("", scale, options, error);
  CHECK(pipeline != nullptr);
  CHECK(error->empty());
  return pipeline;
}

std::string write_input(const std::string& name, const chronos::Image& image) {
  const std::string path = chronos_test::temp_path(name);
  chronos_test::write_image_or_fail(path, image);
  return path;
}

}  // namespace

TEST(pipeline, documented_case_64x48_to_128x96) {
  const chronos::Image source = chronos_test::make_gradient_rgb(64, 48);
  const std::string input = write_input("pipeline_64x48.png", source);
  const std::string output = chronos_test::temp_path("pipeline_64x48_out.png");

  std::string error;
  auto pipeline =
      create_or_fail(make_options(32, 8, 2, CHRONOS_FLAG_NONE), 2, &error);
  CHECK_EQ(pipeline->backend_id(), CHRONOS_BACKEND_STUB);
  CHECK_EQ(pipeline->scale(), 2);
  CHECK(pipeline->describe().find("stub") != std::string::npos);

  int width = 0;
  int height = 0;
  CHECK_EQ(pipeline->process(input, output, &width, &height, &error), CHRONOS_OK);
  CHECK(error.empty());
  CHECK_EQ(width, 128);
  CHECK_EQ(height, 96);
  /* 3 x 2 tiles for tile 32 / overlap 8. */
  CHECK_EQ(pipeline->progress_total(), 6);
  CHECK_EQ(pipeline->progress_done(), 6);

  const chronos::Image result = chronos_test::read_image_or_fail(output, false);
  CHECK_EQ(result.width, 128);
  CHECK_EQ(result.height, 96);
  CHECK_EQ(result.channels, 3);
  chronos_test::remove_file(input);
  chronos_test::remove_file(output);
}

TEST(pipeline, constant_image_stays_bit_exact_zero_variance) {
  /* The seam test: with feather blending a constant image must come out with
   * exactly the same constant value everywhere (no visible seams, no darkening
   * in overlaps). */
  const uint8_t red = 77;
  const uint8_t green = 133;
  const uint8_t blue = 201;
  const chronos::Image source = chronos_test::make_constant_rgb(64, 48, red, green, blue);
  const std::string input = write_input("pipeline_constant.png", source);
  const std::string output = chronos_test::temp_path("pipeline_constant_out.png");

  std::string error;
  auto pipeline = create_or_fail(make_options(32, 8, 3, CHRONOS_FLAG_NONE), 2, &error);
  CHECK_EQ(pipeline->process(input, output, nullptr, nullptr, &error), CHRONOS_OK);

  const chronos::Image result = chronos_test::read_image_or_fail(output, false);
  CHECK_EQ(result.width, 128);
  CHECK_EQ(result.height, 96);
  for (size_t i = 0; i < result.pixels.size(); i += 3) {
    CHECK_EQ(result.pixels[i + 0], red);
    CHECK_EQ(result.pixels[i + 1], green);
    CHECK_EQ(result.pixels[i + 2], blue);
  }
  int minimum = 0;
  int maximum = 0;
  chronos_test::channel_range(result, 0, &minimum, &maximum);
  CHECK_EQ(minimum, static_cast<int>(red));
  CHECK_EQ(maximum, static_cast<int>(red));
  chronos_test::remove_file(input);
  chronos_test::remove_file(output);
}

TEST(pipeline, scale_one_is_identity) {
  const chronos::Image source = chronos_test::make_gradient_rgb(37, 23);
  const std::string input = write_input("pipeline_scale1.png", source);
  const std::string output = chronos_test::temp_path("pipeline_scale1_out.png");

  std::string error;
  auto pipeline = create_or_fail(make_options(16, 4, 2, CHRONOS_FLAG_NONE), 1, &error);
  int width = 0;
  int height = 0;
  CHECK_EQ(pipeline->process(input, output, &width, &height, &error), CHRONOS_OK);
  CHECK_EQ(width, 37);
  CHECK_EQ(height, 23);

  const chronos::Image result = chronos_test::read_image_or_fail(output, false);
  CHECK_EQ(chronos_test::max_abs_diff(result, source), 0);
  chronos_test::remove_file(input);
  chronos_test::remove_file(output);
}

TEST(pipeline, fast_flag_uses_bilinear_and_still_upscales) {
  const chronos::Image source = chronos_test::make_gradient_rgb(32, 24);
  const std::string input = write_input("pipeline_fast.png", source);
  const std::string output = chronos_test::temp_path("pipeline_fast_out.png");

  std::string error;
  auto pipeline = create_or_fail(make_options(16, 4, 2, CHRONOS_FLAG_FAST), 2, &error);
  CHECK(pipeline->describe().find("Bilinear") != std::string::npos);
  int width = 0;
  int height = 0;
  CHECK_EQ(pipeline->process(input, output, &width, &height, &error), CHRONOS_OK);
  CHECK_EQ(width, 64);
  CHECK_EQ(height, 48);
  chronos_test::remove_file(input);
  chronos_test::remove_file(output);
}

TEST(pipeline, preserve_alpha_produces_rgba_output) {
  const chronos::Image source = chronos_test::make_gradient_rgba(20, 10);
  const std::string input = write_input("pipeline_alpha.png", source);
  const std::string output = chronos_test::temp_path("pipeline_alpha_out.png");

  std::string error;
  auto pipeline =
      create_or_fail(make_options(16, 4, 2, CHRONOS_FLAG_PRESERVE_ALPHA), 2, &error);
  int width = 0;
  int height = 0;
  CHECK_EQ(pipeline->process(input, output, &width, &height, &error), CHRONOS_OK);
  CHECK_EQ(width, 40);
  CHECK_EQ(height, 20);

  const chronos::Image result = chronos_test::read_image_or_fail(output, true);
  CHECK_EQ(result.channels, 4);
  CHECK_EQ(result.width, 40);
  CHECK_EQ(result.height, 20);
  /* Alpha ramp is scaled smoothly: corners stay close to the source values. */
  CHECK_NEAR(result.pixels[(0 * 40 + 0) * 4 + 3], 32, 10);
  CHECK_NEAR(result.pixels[(19 * 40 + 39) * 4 + 3], 232, 10);
  /* Alpha must be monotonic along the ramp (allow 2 LSB of edge ringing). */
  for (int y = 0; y < result.height; ++y) {
    for (int x = 1; x < result.width; ++x) {
      CHECK(result.row(y)[static_cast<size_t>(x) * 4 + 3] + 2 >=
            result.row(y)[static_cast<size_t>(x - 1) * 4 + 3]);
    }
  }
  chronos_test::remove_file(input);
  chronos_test::remove_file(output);
}

TEST(pipeline, session_reuse_processes_images_sequentially) {
  const chronos::Image first = chronos_test::make_constant_rgb(30, 20, 10, 20, 30);
  const chronos::Image second = chronos_test::make_constant_rgb(40, 24, 200, 100, 50);
  const std::string first_input = write_input("pipeline_seq_a.png", first);
  const std::string second_input = write_input("pipeline_seq_b.png", second);
  const std::string first_output = chronos_test::temp_path("pipeline_seq_a_out.png");
  const std::string second_output = chronos_test::temp_path("pipeline_seq_b_out.png");

  std::string error;
  auto pipeline = create_or_fail(make_options(16, 4, 2, CHRONOS_FLAG_NONE), 2, &error);

  auto expected_tiles = [](int width, int height) {
    chronos::TilePlan plan;
    std::string plan_error;
    if (chronos::plan_tiles(width, height, 16, 4, 2, &plan, &plan_error) != CHRONOS_OK) return -1;
    return plan.count();
  };

  CHECK_EQ(pipeline->process(first_input, first_output, nullptr, nullptr, &error), CHRONOS_OK);
  CHECK_EQ(pipeline->progress_done(), pipeline->progress_total());
  const int first_total = pipeline->progress_total();
  CHECK_EQ(first_total, expected_tiles(30, 20));
  const chronos::Image first_result = chronos_test::read_image_or_fail(first_output, false);
  CHECK_EQ(first_result.width, 60);
  CHECK_EQ(first_result.height, 40);

  CHECK_EQ(pipeline->process(second_input, second_output, nullptr, nullptr, &error), CHRONOS_OK);
  CHECK_EQ(pipeline->progress_done(), pipeline->progress_total());
  const int second_total = pipeline->progress_total();
  CHECK_EQ(second_total, expected_tiles(40, 24));
  const chronos::Image second_result = chronos_test::read_image_or_fail(second_output, false);
  CHECK_EQ(second_result.width, 80);
  CHECK_EQ(second_result.height, 48);
  CHECK_EQ(second_result.pixels[0], 200);

  chronos_test::remove_file(first_input);
  chronos_test::remove_file(second_input);
  chronos_test::remove_file(first_output);
  chronos_test::remove_file(second_output);
}

TEST(pipeline, cancellation_returns_minus_six) {
  const chronos::Image source = chronos_test::make_gradient_rgb(32, 24);
  const std::string input = write_input("pipeline_cancel.png", source);
  const std::string output = chronos_test::temp_path("pipeline_cancel_out.png");

  std::string error;
  auto pipeline = create_or_fail(make_options(16, 4, 2, CHRONOS_FLAG_NONE), 2, &error);
  CHECK_FALSE(pipeline->cancelled());
  pipeline->cancel();
  CHECK(pipeline->cancelled());
  CHECK_EQ(pipeline->process(input, output, nullptr, nullptr, &error), CHRONOS_ERR_CANCELLED);

  pipeline->reset_progress();
  CHECK_EQ(pipeline->progress_done(), 0);
  CHECK_EQ(pipeline->progress_total(), 0);
  chronos_test::remove_file(input);
  chronos_test::remove_file(output);
}

TEST(pipeline, io_and_parameter_errors) {
  const chronos::Image source = chronos_test::make_gradient_rgb(24, 16);
  const std::string input = write_input("pipeline_errors.png", source);
  const std::string output = chronos_test::temp_path("pipeline_errors_out.png");
  std::string error;
  auto pipeline = create_or_fail(make_options(16, 4, 2, CHRONOS_FLAG_NONE), 2, &error);

  error.clear();
  CHECK_EQ(pipeline->process(chronos_test::temp_path("nope.png"), output, nullptr, nullptr,
                             &error),
           CHRONOS_ERR_IO);
  CHECK(!error.empty());

  error.clear();
  CHECK_EQ(pipeline->process(input, chronos_test::temp_path("bad_extension.txt"), nullptr,
                             nullptr, &error),
           CHRONOS_ERR_IO);
  CHECK(!error.empty());

  error.clear();
  CHECK_EQ(pipeline->process(input, "chronos_missing_dir/out.png", nullptr, nullptr, &error),
           CHRONOS_ERR_IO);
  CHECK(!error.empty());

  /* Bad create() arguments must fail cleanly (never throw). */
  error.clear();
  CHECK(chronos::Pipeline::create("", 5, make_options(16, 4, 2, CHRONOS_FLAG_NONE), &error) ==
        nullptr);
  CHECK(!error.empty());
  error.clear();
  CHECK(chronos::Pipeline::create("", 0, make_options(16, 4, 2, CHRONOS_FLAG_NONE), &error) ==
        nullptr);
  error.clear();
  CHECK(chronos::Pipeline::create("", 2, make_options(4, 2, 2, CHRONOS_FLAG_NONE), &error) ==
        nullptr);
  CHECK(!error.empty());
  error.clear();
  CHECK(chronos::Pipeline::create("", 2, make_options(16, 16, 2, CHRONOS_FLAG_NONE), &error) ==
        nullptr);
  CHECK(!error.empty());
  error.clear();
  CHECK(chronos::Pipeline::create("", 2, make_options(16, 4, 2, 0x400), &error) == nullptr);
  CHECK(!error.empty());
  chronos_test::remove_file(input);
  chronos_test::remove_file(output);
}

TEST(pipeline, zero_options_use_documented_defaults) {
  chronos::Options defaults;
  defaults.tile_size = 0;
  defaults.overlap = 0;
  defaults.threads = 0;
  std::string error;
  auto pipeline = chronos::Pipeline::create("", 1, defaults, &error);
  CHECK(pipeline != nullptr);
  CHECK(error.empty());

  const chronos::Image source = chronos_test::make_gradient_rgb(30, 18);
  const std::string input = write_input("pipeline_defaults.png", source);
  const std::string output = chronos_test::temp_path("pipeline_defaults_out.png");
  int width = 0;
  int height = 0;
  CHECK_EQ(pipeline->process(input, output, &width, &height, &error), CHRONOS_OK);
  CHECK_EQ(width, 30);
  CHECK_EQ(height, 18);
  const chronos::Image result = chronos_test::read_image_or_fail(output, false);
  CHECK_EQ(chronos_test::max_abs_diff(result, source), 0);
  chronos_test::remove_file(input);
  chronos_test::remove_file(output);
}

TEST(pipeline, thread_count_does_not_change_the_result) {
  const chronos::Image source = chronos_test::make_gradient_rgb(48, 32);
  const std::string input = write_input("pipeline_threads.png", source);
  const std::string output_one = chronos_test::temp_path("pipeline_threads_1.png");
  const std::string output_many = chronos_test::temp_path("pipeline_threads_n.png");

  std::string error;
  auto single = create_or_fail(make_options(16, 4, 1, CHRONOS_FLAG_NONE), 2, &error);
  auto many = create_or_fail(make_options(16, 4, 4, CHRONOS_FLAG_NONE), 2, &error);
  CHECK_EQ(single->process(input, output_one, nullptr, nullptr, &error), CHRONOS_OK);
  CHECK_EQ(many->process(input, output_many, nullptr, nullptr, &error), CHRONOS_OK);

  const chronos::Image result_one = chronos_test::read_image_or_fail(output_one, false);
  const chronos::Image result_many = chronos_test::read_image_or_fail(output_many, false);
  /* Only float accumulation order may differ, so allow a 1 LSB difference. */
  CHECK(chronos_test::max_abs_diff(result_one, result_many) <= 1);
  chronos_test::remove_file(input);
  chronos_test::remove_file(output_one);
  chronos_test::remove_file(output_many);
}
