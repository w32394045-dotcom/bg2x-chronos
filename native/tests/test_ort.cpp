/*
 * Project Chronos - ONNX Runtime backend tests.
 *
 * The runtime library is loaded dynamically, so it may legitimately be absent
 * (CI without the runtime, or a user that has not downloaded it yet). In that
 * case every test prints a clear SKIP message and the suite exits with code 77,
 * which CMake maps to ctest's "Skipped" state (SKIP_RETURN_CODE 77). The tests
 * never fail just because the runtime is missing.
 *
 * Coverage with the runtime present:
 *   - loader introspection (library path, version string, execution providers)
 *   - C ABI introspection for CHRONOS_BACKEND_ORT
 *   - the toy model fixture: one tile, output contrast, wrong-size error
 *   - full C ABI round trip: create session -> process image -> 2x output
 *   - tiling invariance: tile 32 vs tile 48 produce the same image
 *   - sidecar model.json: fixedSize, tileAlign, crop mismatch and invalid JSON
 *   - clear error for a missing model and for a scale mismatch
 */
#include <chronos/backend.h>
#include <chronos/chronos.h>
#include <chronos/image.h>
#include <chronos/options.h>
#include <chronos/pipeline.h>

#include <cstddef>
#include <cstdio>
#include <cstring>
#include <memory>
#include <string>
#include <vector>

#include "harness.h"
#include "ort_loader.h"
#include "platform_io.h"
#include "test_util.h"

namespace {

/*
 * The fixture directory comes from ctest (CHRONOS_TEST_FIXTURES_DIR) instead of
 * a compile definition: the repository path may contain non-ASCII characters
 * and the Windows toolchain narrows compile definitions through the ANSI code
 * page. A relative fallback keeps the suite runnable by hand from a build dir.
 */
std::string fixture_directory() {
  std::string directory = chronos::getenv_utf8("CHRONOS_TEST_FIXTURES_DIR");
  if (directory.empty()) directory = "../tests/fixtures";
  return directory;
}

std::string fixture_path() {
  return chronos::join_path(fixture_directory(), "toy_sr_x2.onnx");
}

/* Same toy model with a pinned [1,3,32,32] input. */
std::string static_fixture_path() {
  return chronos::join_path(fixture_directory(), "toy_sr_x2_static32.onnx");
}

/* Skips the current test with an actionable message when ORT is not installed. */
void require_runtime() {
  if (chronos::ort_available()) return;
  SKIP_TEST(
      "ONNX Runtime library not found (set CHRONOS_ORT_DIR to the directory containing "
      "onnxruntime.dll); the native_ort suite is skipped");
}

chronos::Options make_options(int tile, int overlap, int threads) {
  chronos::Options options;
  options.tile_size = tile;
  options.overlap = overlap;
  options.threads = threads;
  return options;
}

std::string read_file_contents(const std::string& path) {
  std::FILE* file = chronos::fopen_utf8(path, "rb");
  CHECK(file != nullptr);
  if (file == nullptr) return std::string();
  std::string contents;
  char buffer[4096];
  for (;;) {
    const size_t read = std::fread(buffer, 1, sizeof(buffer), file);
    if (read > 0) contents.append(buffer, read);
    if (read < sizeof(buffer)) break;
  }
  std::fclose(file);
  return contents;
}

void write_file_contents(const std::string& path, const std::string& contents) {
  std::FILE* file = chronos::fopen_utf8(path, "wb");
  CHECK(file != nullptr);
  if (file == nullptr) return;
  const size_t written = std::fwrite(contents.data(), 1, contents.size(), file);
  std::fclose(file);
  CHECK_EQ(written, contents.size());
}

/* Creates (or recreates) a scratch directory below the ctest working directory. */
std::string fresh_directory(const std::string& name) {
  const std::string directory = chronos_test::temp_path(name);
  chronos::remove_directory_utf8(directory);
  CHECK(chronos::create_directory_utf8(directory));
  return directory;
}

/* Copies the committed toy model next to a scratch sidecar. */
std::string make_sidecar_model(const std::string& directory_name, const std::string& sidecar_json) {
  const std::string directory = fresh_directory(directory_name);
  const std::string model = chronos::join_path(directory, "model.onnx");
  const std::string contents = read_file_contents(fixture_path());
  CHECK(!contents.empty());
  write_file_contents(model, contents);
  write_file_contents(chronos::join_path(directory, "model.json"), sidecar_json);
  return model;
}

void remove_sidecar_model(const std::string& model_path) {
  const std::string directory = chronos::directory_of(model_path);
  chronos::remove_file_utf8(model_path);
  chronos::remove_file_utf8(chronos::join_path(directory, "model.json"));
  chronos::remove_directory_utf8(directory);
}

/* Runs one tile through a backend and returns the u8 contrast (max - min). */
int tile_contrast(chronos::IBackend& backend, int tile_size, std::string* error) {
  const int patch_size = tile_size * backend.native_scale();
  std::vector<uint8_t> tile(static_cast<size_t>(tile_size) * static_cast<size_t>(tile_size) * 3);
  for (int y = 0; y < tile_size; ++y) {
    for (int x = 0; x < tile_size; ++x) {
      uint8_t* pixel = tile.data() +
                       (static_cast<size_t>(y) * static_cast<size_t>(tile_size) +
                        static_cast<size_t>(x)) *
                           3;
      pixel[0] = static_cast<uint8_t>((x * 255) / (tile_size > 1 ? tile_size - 1 : 1));
      pixel[1] = static_cast<uint8_t>((y * 255) / (tile_size > 1 ? tile_size - 1 : 1));
      pixel[2] = static_cast<uint8_t>(((x + y) * 255) / (2 * (tile_size > 1 ? tile_size - 1 : 1)));
    }
  }
  std::vector<uint8_t> patch(static_cast<size_t>(patch_size) * static_cast<size_t>(patch_size) * 3,
                             0);
  chronos::TileWorkspace workspace;
  if (!backend.infer_tile(tile.data(), tile_size, patch.data(), patch_size, workspace, error)) {
    return -1;
  }
  if (!error->empty()) return -1;
  int minimum = 255;
  int maximum = 0;
  for (uint8_t value : patch) {
    if (value < minimum) minimum = value;
    if (value > maximum) maximum = value;
  }
  return maximum - minimum;
}

}  // namespace

TEST(ort, loader_reports_runtime) {
  require_runtime();
  CHECK(chronos::ort_available());
  const std::string library = chronos::ort_library_path();
  const std::string version = chronos::ort_version_string();
  const std::string providers = chronos::ort_providers();
  CHECK(!library.empty());
  CHECK(!version.empty());
  CHECK(providers.find("CPU") != std::string::npos);
  std::printf("ort: library=%s version=%s providers=%s\n", library.c_str(), version.c_str(),
              providers.c_str());

  /* C ABI introspection: name, runtime availability and the capability probe. */
  CHECK(std::strcmp(chronos_backend_name(CHRONOS_BACKEND_ORT), "ort") == 0);
  CHECK_EQ(chronos_backend_available(CHRONOS_BACKEND_ORT), 1);
  CHECK_EQ(chronos_ort_available(), 1);
  CHECK_EQ(chronos_abi_version(), 1);

  const int probe_length = chronos_backend_probe(nullptr, 0);
  CHECK(probe_length > 0);
  std::string probe(static_cast<size_t>(probe_length) + 1, '\0');
  CHECK_EQ(chronos_backend_probe(&probe[0], probe_length + 1), probe_length);
  CHECK(probe.find(version) != std::string::npos);
  CHECK(probe.find("\"available\":true") != std::string::npos);
  CHECK(probe.find("CPU") != std::string::npos);
  std::printf("ort: probe=%s\n", probe.c_str());
}

TEST(ort, factory_loads_toy_model_and_runs_one_tile) {
  require_runtime();
  std::string error;
  std::unique_ptr<chronos::IBackend> backend =
      chronos::create_ort_backend(fixture_path(), 2, make_options(32, 8, 2), &error);
  if (backend == nullptr) std::printf("ort: create_ort_backend error: %s\n", error.c_str());
  CHECK(backend != nullptr);
  CHECK(error.empty());
  if (!backend) return;
  CHECK_EQ(backend->id(), CHRONOS_BACKEND_ORT);
  CHECK_EQ(backend->native_scale(), 2);
  CHECK(backend->describe().rfind("ort/", 0) == 0);
  CHECK_FALSE(backend->supports_batch());
  const int tile_size = backend->tile_size_for(32);
  CHECK_EQ(tile_size, 32);

  std::string infer_error;
  const int contrast = tile_contrast(*backend, tile_size, &infer_error);
  CHECK(infer_error.empty());
  CHECK(contrast >= 5);

  /* A wrong patch size must produce a clear error, never a crash. */
  const int patch_size = tile_size * backend->native_scale();
  std::vector<uint8_t> tile(static_cast<size_t>(tile_size) * static_cast<size_t>(tile_size) * 3,
                            120);
  std::vector<uint8_t> wrong_patch(
      (static_cast<size_t>(patch_size) + 1) * static_cast<size_t>(patch_size + 1) * 3, 0);
  chronos::TileWorkspace workspace;
  std::string wrong_error;
  CHECK_FALSE(backend->infer_tile(tile.data(), tile_size, wrong_patch.data(), patch_size + 1,
                                  workspace, &wrong_error));
  CHECK(!wrong_error.empty());
}

TEST(ort, c_api_session_roundtrip) {
  require_runtime();
  chronos_options options;
  std::memset(&options, 0, sizeof(options));
  options.struct_size = static_cast<int>(sizeof(options));
  options.tile_size = 32;
  options.overlap = 8;
  options.threads = 2;

  const std::string model = fixture_path();
  char error[1024];
  error[0] = '\0';
  chronos_session* session =
      chronos_session_create(model.c_str(), 2, &options, error, static_cast<int>(sizeof(error)));
  CHECK(session != nullptr);
  if (session == nullptr) {
    std::printf("ort: chronos_session_create error: %s\n", error);
    return;
  }
  CHECK_EQ(chronos_session_backend(session), CHRONOS_BACKEND_ORT);
  CHECK_EQ(chronos_session_scale(session), 2);
  char description[128];
  description[0] = '\0';
  const int description_length =
      chronos_session_describe(session, description, static_cast<int>(sizeof(description)));
  CHECK(description_length > 0);
  CHECK(std::strstr(description, "ort/") != nullptr);

  const chronos::Image source = chronos_test::make_gradient_rgb(40, 24);
  const std::string input = chronos_test::temp_path("ort_c_api_input.png");
  const std::string output = chronos_test::temp_path("ort_c_api_output.png");
  chronos_test::write_image_or_fail(input, source);

  int width = 0;
  int height = 0;
  error[0] = '\0';
  CHECK_EQ(chronos_process(session, input.c_str(), output.c_str(), &width, &height, error,
                           static_cast<int>(sizeof(error))),
           CHRONOS_OK);
  CHECK_EQ(width, 80);
  CHECK_EQ(height, 48);

  const chronos::Image result = chronos_test::read_image_or_fail(output, false);
  CHECK_EQ(result.width, 80);
  CHECK_EQ(result.height, 48);
  int minimum = 0;
  int maximum = 0;
  chronos_test::channel_range(result, 0, &minimum, &maximum);
  CHECK(maximum - minimum >= 5);

  chronos_session_free(session);
  chronos_test::remove_file(input);
  chronos_test::remove_file(output);
}

TEST(ort, tiling_invariance_tile_32_vs_48) {
  require_runtime();
  const chronos::Image source = chronos_test::make_gradient_rgb(64, 48);
  const std::string input = chronos_test::temp_path("ort_tiling_input.png");
  const std::string output_32 = chronos_test::temp_path("ort_tiling_32.png");
  const std::string output_48 = chronos_test::temp_path("ort_tiling_48.png");
  chronos_test::write_image_or_fail(input, source);

  auto run = [&](int tile, int overlap, const std::string& output) {
    std::string error;
    std::unique_ptr<chronos::Pipeline> pipeline =
        chronos::Pipeline::create(fixture_path(), 2, make_options(tile, overlap, 2), &error);
    CHECK(pipeline != nullptr);
    CHECK(error.empty());
    if (!pipeline) {
      std::printf("ort: pipeline error (tile %d): %s\n", tile, error.c_str());
      return chronos::Image();
    }
    CHECK_EQ(pipeline->backend_id(), CHRONOS_BACKEND_ORT);
    CHECK_EQ(pipeline->process(input, output, nullptr, nullptr, &error), CHRONOS_OK);
    CHECK(error.empty());
    CHECK_EQ(pipeline->progress_done(), pipeline->progress_total());
    return chronos_test::read_image_or_fail(output, false);
  };

  const chronos::Image result_32 = run(32, 8, output_32);
  const chronos::Image result_48 = run(48, 12, output_48);
  CHECK_EQ(result_32.width, 128);
  CHECK_EQ(result_32.height, 96);
  CHECK_EQ(result_48.width, 128);
  CHECK_EQ(result_48.height, 96);

  /* The model is fully convolutional and both runs feed real pixels (no
   * padding at the image border for this size), so the only difference left is
   * float accumulation order: allow 3 LSB. */
  const int difference = chronos_test::max_abs_diff(result_32, result_48);
  std::printf("ort: tiling invariance max abs diff = %d (u8)\n", difference);
  CHECK(difference <= 3);

  chronos_test::remove_file(input);
  chronos_test::remove_file(output_32);
  chronos_test::remove_file(output_48);
}

TEST(ort, sidecar_fixed_size_and_alignment) {
  require_runtime();

  /* fixedSize wins over the requested tile size; tileAlign rounds up. */
  const std::string fixed_model = make_sidecar_model(
      "ort_sidecar_fixed",
      R"({"outputs":[{"name":"","scale":2,"crop":0}],"constraints":{"tileAlign":16,"fixedSize":48}})");
  std::string error;
  std::unique_ptr<chronos::IBackend> fixed_backend =
      chronos::create_ort_backend(fixed_model, 2, make_options(32, 8, 2), &error);
  CHECK(fixed_backend != nullptr);
  CHECK(error.empty());
  if (fixed_backend) {
    CHECK_EQ(fixed_backend->tile_size_for(32), 48);
    CHECK_EQ(fixed_backend->tile_size_for(100), 48);
    CHECK_EQ(fixed_backend->native_scale(), 2);
    std::string infer_error;
    CHECK(tile_contrast(*fixed_backend, fixed_backend->tile_size_for(32), &infer_error) >= 5);
    CHECK(infer_error.empty());
  }
  remove_sidecar_model(fixed_model);

  const std::string aligned_model = make_sidecar_model(
      "ort_sidecar_align", R"({"constraints":{"tileAlign":16}})");
  std::unique_ptr<chronos::IBackend> aligned_backend =
      chronos::create_ort_backend(aligned_model, 2, make_options(30, 8, 2), &error);
  CHECK(aligned_backend != nullptr);
  CHECK(error.empty());
  if (aligned_backend) {
    CHECK_EQ(aligned_backend->tile_size_for(30), 32);
    CHECK_EQ(aligned_backend->tile_size_for(32), 32);
    CHECK_EQ(aligned_backend->tile_size_for(16), 16);
    CHECK_EQ(aligned_backend->tile_size_for(1), 16);
    std::string infer_error;
    CHECK(tile_contrast(*aligned_backend, aligned_backend->tile_size_for(30), &infer_error) >= 5);
    CHECK(infer_error.empty());
  }
  remove_sidecar_model(aligned_model);
}

TEST(ort, sidecar_crop_mismatch_is_reported) {
  require_runtime();
  /* The toy model has no extra border, so crop 4 cannot match: the model loads
   * (dynamic shapes) but the first tile must fail with an explicit message. */
  const std::string model = make_sidecar_model(
      "ort_sidecar_crop", R"({"outputs":[{"name":"","scale":2,"crop":4}]})");
  std::string error;
  std::unique_ptr<chronos::IBackend> backend =
      chronos::create_ort_backend(model, 2, make_options(32, 8, 2), &error);
  CHECK(backend != nullptr);
  if (backend) {
    std::string infer_error;
    const int contrast = tile_contrast(*backend, 32, &infer_error);
    CHECK_EQ(contrast, -1);
    CHECK(!infer_error.empty());
    CHECK(infer_error.find("crop") != std::string::npos);
    std::printf("ort: crop mismatch error: %s\n", infer_error.c_str());
  }
  remove_sidecar_model(model);
}

TEST(ort, invalid_sidecar_is_reported) {
  require_runtime();
  const std::string model =
      make_sidecar_model("ort_sidecar_bad", "{ this is not a json document");
  std::string error;
  std::unique_ptr<chronos::IBackend> backend =
      chronos::create_ort_backend(model, 2, make_options(32, 8, 2), &error);
  CHECK(backend == nullptr);
  CHECK(error.find("model.json") != std::string::npos);
  std::printf("ort: invalid sidecar error: %s\n", error.c_str());
  remove_sidecar_model(model);
}

TEST(ort, missing_model_reports_clear_error) {
  require_runtime();
  std::string error;
  std::unique_ptr<chronos::IBackend> backend =
      chronos::create_ort_backend("chronos_missing_toy.onnx", 2, make_options(32, 8, 2), &error);
  CHECK(backend == nullptr);
  CHECK(!error.empty());
  CHECK(error.find("cannot open ONNX model file") != std::string::npos ||
        error.find("cannot load ONNX model") != std::string::npos);
  std::printf("ort: missing model error: %s\n", error.c_str());

  /* Through the C ABI the message must name both attempts and the fix; the
   * frozen create() signature returns NULL (the message carries the detail). */
  char buffer[1024];
  buffer[0] = '\0';
  CHECK(chronos_session_create("chronos_missing_toy.onnx", 2, nullptr, buffer,
                               static_cast<int>(sizeof(buffer))) == nullptr);
  CHECK(std::strlen(buffer) > 0);
  CHECK(std::strstr(buffer, "cannot load model") != nullptr);
  CHECK(std::strstr(buffer, "ONNX Runtime") != nullptr);
  CHECK(std::strstr(buffer, "OpenVINO") != nullptr);
  std::printf("ort: C ABI error: %s\n", buffer);
}

TEST(ort, scale_mismatch_is_reported) {
  require_runtime();
  /* The toy model has dynamic shapes, so its 2x factor is only statically known
   * through the sidecar; a mismatching request must be rejected at load time. */
  const std::string model = make_sidecar_model(
      "ort_scale_mismatch", R"({"outputs":[{"name":"","scale":2,"crop":0}]})");
  std::string error;
  std::unique_ptr<chronos::IBackend> backend =
      chronos::create_ort_backend(model, 3, make_options(32, 8, 2), &error);
  CHECK(backend == nullptr);
  CHECK(error.find("does not match model scale") != std::string::npos);
  std::printf("ort: scale mismatch error: %s\n", error.c_str());
  remove_sidecar_model(model);
}

TEST(ort, static_model_pins_tile_size_and_scale) {
  require_runtime();
  /* requested_scale 0 means "use the model's native scale": the static
   * output/input ratio (64/32) must yield scale 2. */
  std::string error;
  std::unique_ptr<chronos::IBackend> backend =
      chronos::create_ort_backend(static_fixture_path(), 0, make_options(48, 8, 2), &error);
  if (backend == nullptr) std::printf("ort: static model error: %s\n", error.c_str());
  CHECK(backend != nullptr);
  CHECK(error.empty());
  if (backend) {
    /* A static model input pins the tile size regardless of the request. */
    CHECK_EQ(backend->tile_size_for(48), 32);
    CHECK_EQ(backend->tile_size_for(256), 32);
    CHECK_EQ(backend->native_scale(), 2);
    std::string infer_error;
    CHECK(tile_contrast(*backend, backend->tile_size_for(48), &infer_error) >= 5);
    CHECK(infer_error.empty());
  }

  /* A requested scale that disagrees with the static ratio is rejected. */
  std::string mismatch_error;
  std::unique_ptr<chronos::IBackend> mismatch =
      chronos::create_ort_backend(static_fixture_path(), 4, make_options(32, 8, 2),
                                  &mismatch_error);
  CHECK(mismatch == nullptr);
  CHECK(mismatch_error.find("does not match model scale") != std::string::npos);

  /* End to end through the C ABI with scale 0 (model native scale). */
  chronos_options options;
  std::memset(&options, 0, sizeof(options));
  options.struct_size = static_cast<int>(sizeof(options));
  options.tile_size = 48;
  options.overlap = 8;
  options.threads = 2;
  const std::string model = static_fixture_path();
  char error_buffer[1024];
  error_buffer[0] = '\0';
  chronos_session* session =
      chronos_session_create(model.c_str(), 0, &options, error_buffer,
                             static_cast<int>(sizeof(error_buffer)));
  CHECK(session != nullptr);
  if (session != nullptr) {
    CHECK_EQ(chronos_session_backend(session), CHRONOS_BACKEND_ORT);
    CHECK_EQ(chronos_session_scale(session), 2);
    const chronos::Image source = chronos_test::make_gradient_rgb(64, 48);
    const std::string input = chronos_test::temp_path("ort_static_input.png");
    const std::string output = chronos_test::temp_path("ort_static_output.png");
    chronos_test::write_image_or_fail(input, source);
    int width = 0;
    int height = 0;
    CHECK_EQ(chronos_process(session, input.c_str(), output.c_str(), &width, &height, error_buffer,
                             static_cast<int>(sizeof(error_buffer))),
             CHRONOS_OK);
    CHECK_EQ(width, 128);
    CHECK_EQ(height, 96);
    chronos_test::remove_file(input);
    chronos_test::remove_file(output);
    chronos_session_free(session);
  }
}

TEST(ort, sidecar_norm_modes_are_accepted) {
  require_runtime();
  const char* const norms[] = {"0-1", "0-255", "-1-1"};
  for (const char* norm : norms) {
    const std::string json =
        std::string("{\"preprocess\":{\"norm\":\"") + norm +
        "\",\"mean\":[0.5,0.5,0.5],\"std\":[0.5,0.5,0.5]}}";
    const std::string model = make_sidecar_model(std::string("ort_norm_") + norm, json);
    std::string error;
    std::unique_ptr<chronos::IBackend> backend =
        chronos::create_ort_backend(model, 2, make_options(32, 8, 2), &error);
    if (backend == nullptr) std::printf("ort: norm %s error: %s\n", norm, error.c_str());
    CHECK(backend != nullptr);
    CHECK(error.empty());
    if (backend) {
      std::string infer_error;
      CHECK(tile_contrast(*backend, 32, &infer_error) >= 0);
      CHECK(infer_error.empty());
    }
    remove_sidecar_model(model);
  }

  const std::string bad_model =
      make_sidecar_model("ort_norm_bad", R"({"preprocess":{"norm":"scaled"}})");
  std::string bad_error;
  std::unique_ptr<chronos::IBackend> bad_backend =
      chronos::create_ort_backend(bad_model, 2, make_options(32, 8, 2), &bad_error);
  CHECK(bad_backend == nullptr);
  CHECK(bad_error.find("preprocess.norm") != std::string::npos);
  remove_sidecar_model(bad_model);
}

TEST(ort, sidecar_tensor_names) {
  require_runtime();
  /* Explicit names that match the model must be accepted. */
  const std::string good = make_sidecar_model(
      "ort_names_ok",
      R"({"inputs":[{"name":"input","source":"image"}],"outputs":[{"name":"output","scale":2}]})");
  std::string error;
  std::unique_ptr<chronos::IBackend> backend =
      chronos::create_ort_backend(good, 2, make_options(32, 8, 2), &error);
  if (backend == nullptr) std::printf("ort: names error: %s\n", error.c_str());
  CHECK(backend != nullptr);
  CHECK(error.empty());
  if (backend) {
    std::string infer_error;
    CHECK(tile_contrast(*backend, 32, &infer_error) >= 0);
    CHECK(infer_error.empty());
  }
  remove_sidecar_model(good);

  /* A name that is not present must be reported with the model's real names. */
  const std::string bad = make_sidecar_model(
      "ort_names_bad", R"({"outputs":[{"name":"missing_output"}]})");
  std::string bad_error;
  std::unique_ptr<chronos::IBackend> bad_backend =
      chronos::create_ort_backend(bad, 2, make_options(32, 8, 2), &bad_error);
  CHECK(bad_backend == nullptr);
  CHECK(bad_error.find("not found in the model") != std::string::npos);
  std::printf("ort: unknown tensor name error: %s\n", bad_error.c_str());
  remove_sidecar_model(bad);
}
