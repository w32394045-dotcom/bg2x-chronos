/*
 * Project Chronos — C ABI tests.
 *
 * Covers: ABI/version/backend introspection, thread-local last_error, option
 * validation, session lifecycle, process success and every documented error
 * code path (-1 IO, -4 PARAM, -6 CANCELLED), error-buffer termination and
 * exception safety (no crash on garbage input).
 */
#include <chronos/chronos.h>

#include <chronos/image.h>

#include <cstddef>
#include <cstring>
#include <fstream>
#include <string>

#include "harness.h"
#include "test_util.h"

namespace {

chronos_options default_options() {
  chronos_options options;
  std::memset(&options, 0, sizeof(options));
  options.struct_size = static_cast<int>(sizeof(chronos_options));
  options.tile_size = 16;
  options.overlap = 4;
  options.threads = 2;
  return options;
}

/* Creates a session and fails the test when it cannot. */
chronos_session* create_or_fail(int scale, const chronos_options* options) {
  char error[512];
  error[0] = '\0';
  chronos_session* session =
      chronos_session_create(nullptr, scale, options, error, static_cast<int>(sizeof(error)));
  CHECK(session != nullptr);
  CHECK_EQ(error[0], '\0');
  return session;
}

}  // namespace

TEST(c_api, abi_and_backend_introspection) {
  CHECK_EQ(chronos_abi_version(), CHRONOS_ABI_VERSION);
  CHECK_EQ(chronos_abi_version(), 1);

  const char* version = chronos_version_string();
  CHECK(version != nullptr);
  CHECK(std::strlen(version) > 0);
  CHECK(std::strstr(version, "chronos") != nullptr);

  CHECK(std::strcmp(chronos_backend_name(CHRONOS_BACKEND_NONE), "none") == 0);
  CHECK(std::strcmp(chronos_backend_name(CHRONOS_BACKEND_STUB), "stub") == 0);
  CHECK(std::strcmp(chronos_backend_name(CHRONOS_BACKEND_OPENVINO), "openvino") == 0);
  /* Unknown ids must still return a non-NULL, printable name. */
  CHECK(chronos_backend_name(1234) != nullptr);

  CHECK_EQ(chronos_backend_available(CHRONOS_BACKEND_STUB), 1);
  CHECK_EQ(chronos_backend_available(CHRONOS_BACKEND_NONE), 0);
  const int openvino = chronos_backend_available(CHRONOS_BACKEND_OPENVINO);
  CHECK(openvino == 0 || openvino == 1);
  CHECK(chronos_last_error() != nullptr);
}

TEST(c_api, session_lifecycle_and_describe) {
  const chronos_options options = default_options();
  char error[512];
  error[0] = '\0';
  chronos_session* session =
      chronos_session_create(nullptr, 2, &options, error, static_cast<int>(sizeof(error)));
  CHECK(session != nullptr);
  CHECK_EQ(chronos_session_backend(session), CHRONOS_BACKEND_STUB);
  CHECK_EQ(chronos_session_scale(session), 2);
  CHECK_EQ(chronos_session_cancelled(session), 0);

  char description[128];
  description[0] = '\0';
  const int length = chronos_session_describe(session, description,
                                              static_cast<int>(sizeof(description)));
  CHECK(length > 0);
  CHECK(std::strlen(description) == static_cast<size_t>(length));
  CHECK(std::strstr(description, "stub") != nullptr);

  /* NULL buffer asks for the required length. */
  CHECK_EQ(chronos_session_describe(session, nullptr, 0), length);

  /* Tiny buffer: truncated but NUL-terminated. */
  char tiny[3];
  tiny[0] = 'x';
  const int tiny_length = chronos_session_describe(session, tiny, static_cast<int>(sizeof(tiny)));
  CHECK(tiny_length >= 0);
  CHECK(tiny_length <= static_cast<int>(sizeof(tiny)) - 1);
  CHECK_EQ(tiny[tiny_length], '\0');

  int done = -1;
  int total = -1;
  chronos_session_progress(session, &done, &total);
  CHECK_EQ(done, 0);
  CHECK_EQ(total, 0);

  chronos_session_free(session);
  /* Freeing NULL must be safe. */
  chronos_session_free(nullptr);

  /* NULL session accessors report CHRONOS_ERR_PARAM without crashing. */
  CHECK_EQ(chronos_session_backend(nullptr), CHRONOS_ERR_PARAM);
  CHECK_EQ(chronos_session_scale(nullptr), CHRONOS_ERR_PARAM);
  CHECK_EQ(chronos_session_cancelled(nullptr), 0);
  int done_null = 5;
  int total_null = 5;
  chronos_session_progress(nullptr, &done_null, &total_null);
  CHECK_EQ(done_null, 0);
  CHECK_EQ(total_null, 0);
  chronos_session_reset_progress(nullptr);
  chronos_session_cancel(nullptr);
}

TEST(c_api, options_validation) {
  char error[512];

  /* Wrong struct_size. */
  chronos_options bad = default_options();
  bad.struct_size = 3;
  error[0] = '\0';
  CHECK(chronos_session_create(nullptr, 2, &bad, error, static_cast<int>(sizeof(error))) ==
        nullptr);
  CHECK(!std::string(error).empty());
  CHECK(std::strlen(chronos_last_error()) > 0);

  /* Non-zero reserved fields are rejected (contract: must be zeroed). */
  bad = default_options();
  bad.reserved[0] = 1;
  error[0] = '\0';
  CHECK(chronos_session_create(nullptr, 2, &bad, error, static_cast<int>(sizeof(error))) ==
        nullptr);
  CHECK(!std::string(error).empty());

  /* Unknown flag bits. */
  bad = default_options();
  bad.flags = 0x400;
  error[0] = '\0';
  CHECK(chronos_session_create(nullptr, 2, &bad, error, static_cast<int>(sizeof(error))) ==
        nullptr);
  CHECK(!std::string(error).empty());

  /* Tile size below the sanity floor. */
  bad = default_options();
  bad.tile_size = 4;
  error[0] = '\0';
  CHECK(chronos_session_create(nullptr, 2, &bad, error, static_cast<int>(sizeof(error))) ==
        nullptr);
  CHECK(!std::string(error).empty());

  /* overlap >= tile_size. */
  bad = default_options();
  bad.overlap = bad.tile_size;
  error[0] = '\0';
  CHECK(chronos_session_create(nullptr, 2, &bad, error, static_cast<int>(sizeof(error))) ==
        nullptr);
  CHECK(!std::string(error).empty());

  /* struct_size == 0 means "defaults" per the frozen header. */
  bad = default_options();
  bad.struct_size = 0;
  bad.tile_size = 0;
  bad.overlap = 0;
  bad.threads = 0;
  error[0] = '\0';
  chronos_session* session =
      chronos_session_create(nullptr, 1, &bad, error, static_cast<int>(sizeof(error)));
  CHECK(session != nullptr);
  CHECK_EQ(chronos_session_scale(session), 1);
  chronos_session_free(session);

  /* Options NULL also means defaults. */
  CHECK(create_or_fail(3, nullptr) != nullptr);
}

TEST(c_api, unsupported_scale_and_model_path) {
  char error[512];

  error[0] = '\0';
  CHECK(chronos_session_create(nullptr, 5, nullptr, error, static_cast<int>(sizeof(error))) ==
        nullptr);
  CHECK(!std::string(error).empty());

  error[0] = '\0';
  CHECK(chronos_session_create(nullptr, 0, nullptr, error, static_cast<int>(sizeof(error))) ==
        nullptr);
  CHECK(!std::string(error).empty());

  /* A model path in a build without OpenVINO must fail cleanly and say so. */
  error[0] = '\0';
  const char* model = "chronos_missing_model.xml";
  CHECK(chronos_session_create(model, 4, nullptr, error, static_cast<int>(sizeof(error))) ==
        nullptr);
  CHECK(!std::string(error).empty());
  CHECK(std::strstr(error, "OpenVINO") != nullptr || std::strstr(error, "openvino") != nullptr);
  CHECK(std::strlen(chronos_last_error()) > 0);
}

TEST(c_api, process_success_and_progress) {
  const chronos::Image source = chronos_test::make_constant_rgb(24, 16, 90, 120, 150);
  const std::string input = chronos_test::temp_path("c_api_input.png");
  const std::string output = chronos_test::temp_path("c_api_output.png");
  chronos_test::write_image_or_fail(input, source);

  chronos_options options = default_options();
  chronos_session* session = create_or_fail(2, &options);

  char error[512];
  error[0] = '\0';
  int width = 0;
  int height = 0;
  CHECK_EQ(chronos_process(session, input.c_str(), output.c_str(), &width, &height, error,
                           static_cast<int>(sizeof(error))),
           CHRONOS_OK);
  CHECK_EQ(width, 48);
  CHECK_EQ(height, 32);
  CHECK_EQ(error[0], '\0');
  CHECK_EQ(std::strlen(chronos_last_error()), static_cast<size_t>(0));

  int done = 0;
  int total = 0;
  chronos_session_progress(session, &done, &total);
  CHECK(total > 0);
  CHECK_EQ(done, total);

  const chronos::Image result = chronos_test::read_image_or_fail(output, false);
  CHECK_EQ(result.width, 48);
  CHECK_EQ(result.height, 32);
  CHECK_EQ(result.pixels[0], 90);
  int minimum = 0;
  int maximum = 0;
  chronos_test::channel_range(result, 0, &minimum, &maximum);
  CHECK_EQ(minimum, 90);
  CHECK_EQ(maximum, 90);

  /* Optional out_width/out_height may be NULL. */
  error[0] = '\0';
  CHECK_EQ(chronos_process(session, input.c_str(), output.c_str(), nullptr, nullptr, error,
                           static_cast<int>(sizeof(error))),
           CHRONOS_OK);

  chronos_session_reset_progress(session);
  chronos_session_progress(session, &done, &total);
  CHECK_EQ(done, 0);
  CHECK_EQ(total, 0);

  chronos_session_free(session);
  chronos_test::remove_file(input);
  chronos_test::remove_file(output);
}

TEST(c_api, process_argument_and_io_errors) {
  const chronos::Image source = chronos_test::make_constant_rgb(16, 12, 1, 2, 3);
  const std::string input = chronos_test::temp_path("c_api_errors_input.png");
  const std::string output = chronos_test::temp_path("c_api_errors_output.png");
  chronos_test::write_image_or_fail(input, source);

  const chronos_options options = default_options();
  chronos_session* session = create_or_fail(2, &options);
  char error[512];

  /* NULL arguments -> -4. */
  error[0] = '\0';
  CHECK_EQ(chronos_process(nullptr, input.c_str(), output.c_str(), nullptr, nullptr, error,
                          static_cast<int>(sizeof(error))),
           CHRONOS_ERR_PARAM);
  CHECK(!std::string(error).empty());
  error[0] = '\0';
  CHECK_EQ(chronos_process(session, nullptr, output.c_str(), nullptr, nullptr, error,
                           static_cast<int>(sizeof(error))),
           CHRONOS_ERR_PARAM);
  CHECK(!std::string(error).empty());
  error[0] = '\0';
  CHECK_EQ(chronos_process(session, input.c_str(), nullptr, nullptr, nullptr, error,
                           static_cast<int>(sizeof(error))),
           CHRONOS_ERR_PARAM);
  CHECK(!std::string(error).empty());
  error[0] = '\0';
  CHECK_EQ(chronos_process(session, "", output.c_str(), nullptr, nullptr, error,
                           static_cast<int>(sizeof(error))),
           CHRONOS_ERR_PARAM);
  CHECK(!std::string(error).empty());

  /* Missing input file -> -1 IO. */
  error[0] = '\0';
  CHECK_EQ(chronos_process(session, chronos_test::temp_path("missing_input.png").c_str(),
                           output.c_str(), nullptr, nullptr, error,
                           static_cast<int>(sizeof(error))),
           CHRONOS_ERR_IO);
  CHECK(!std::string(error).empty());
  CHECK(std::strlen(chronos_last_error()) > 0);

  /* Unsupported output extension -> -1 IO. */
  error[0] = '\0';
  CHECK_EQ(chronos_process(session, input.c_str(), chronos_test::temp_path("out.txt").c_str(),
                           nullptr, nullptr, error, static_cast<int>(sizeof(error))),
           CHRONOS_ERR_IO);
  CHECK(!std::string(error).empty());

  /* A directory as input -> IO error, never an exception. */
  error[0] = '\0';
  const int directory_rc = chronos_process(session, ".", output.c_str(), nullptr, nullptr, error,
                                           static_cast<int>(sizeof(error)));
  CHECK(directory_rc != CHRONOS_OK);
  CHECK(directory_rc == CHRONOS_ERR_IO || directory_rc == CHRONOS_ERR_PARAM);

  chronos_session_free(session);
  chronos_test::remove_file(input);
  chronos_test::remove_file(output);
}

TEST(c_api, cancellation_and_error_buffer_termination) {
  const chronos::Image source = chronos_test::make_gradient_rgb(32, 24);
  const std::string input = chronos_test::temp_path("c_api_cancel_input.png");
  const std::string output = chronos_test::temp_path("c_api_cancel_output.png");
  chronos_test::write_image_or_fail(input, source);

  const chronos_options options = default_options();
  chronos_session* session = create_or_fail(2, &options);
  CHECK_EQ(chronos_session_cancelled(session), 0);
  chronos_session_cancel(session);
  CHECK_EQ(chronos_session_cancelled(session), 1);

  char error[512];
  error[0] = '\0';
  CHECK_EQ(chronos_process(session, input.c_str(), output.c_str(), nullptr, nullptr, error,
                           static_cast<int>(sizeof(error))),
           CHRONOS_ERR_CANCELLED);
  CHECK(!std::string(error).empty());
  chronos_session_free(session);

  /* Error buffers are always NUL-terminated, even when tiny. */
  char tiny[4];
  std::memset(tiny, 'x', sizeof(tiny));
  chronos_options bad = default_options();
  bad.struct_size = 1;
  CHECK(chronos_session_create(nullptr, 2, &bad, tiny, static_cast<int>(sizeof(tiny))) ==
        nullptr);
  CHECK_EQ(tiny[sizeof(tiny) - 1], '\0');
  CHECK(std::strlen(tiny) <= sizeof(tiny) - 1);

  /* NULL/zero-length error buffers are tolerated. */
  CHECK(chronos_session_create(nullptr, 2, &bad, nullptr, 0) == nullptr);
  CHECK(chronos_session_create(nullptr, 2, &bad, tiny, 0) == nullptr);

  chronos_test::remove_file(input);
  chronos_test::remove_file(output);
}
