/*
 * Project Chronos — concurrency tests.
 *
 * Proves the "one session per worker thread" contract: two threads each own a
 * session and process images concurrently with no shared mutable state, and the
 * last-error string is thread-local (one thread's failure never leaks into the
 * other thread's view).
 */
#include <chronos/chronos.h>
#include <chronos/image.h>

#include <atomic>
#include <chrono>
#include <cstddef>
#include <cstring>
#include <functional>
#include <string>
#include <thread>
#include <vector>

#include "harness.h"
#include "test_util.h"

namespace {

struct ThreadOutcome {
  bool ok = false;
  std::string message;
};

void run_guarded(ThreadOutcome* outcome, const std::function<void()>& body) {
  try {
    body();
    outcome->ok = true;
  } catch (const std::exception& exception) {
    outcome->ok = false;
    outcome->message = exception.what();
  } catch (...) {
    outcome->ok = false;
    outcome->message = "unknown exception in worker thread";
  }
}

void process_in_thread(const chronos::Image& source, const std::string& input,
                       const std::string& output, int scale, int tile, int overlap,
                       ThreadOutcome* outcome) {
  run_guarded(outcome, [&]() {
    chronos_options options;
    std::memset(&options, 0, sizeof(options));
    options.struct_size = static_cast<int>(sizeof(chronos_options));
    options.tile_size = tile;
    options.overlap = overlap;
    options.threads = 2;

    char error[512];
    error[0] = '\0';
    chronos_session* session =
        chronos_session_create(nullptr, scale, &options, error, static_cast<int>(sizeof(error)));
    CHECK(session != nullptr);
    CHECK_EQ(error[0], '\0');

    for (int iteration = 0; iteration < 3; ++iteration) {
      int width = 0;
      int height = 0;
      error[0] = '\0';
      CHECK_EQ(chronos_process(session, input.c_str(), output.c_str(), &width, &height, error,
                               static_cast<int>(sizeof(error))),
               CHRONOS_OK);
      CHECK_EQ(width, source.width * scale);
      CHECK_EQ(height, source.height * scale);
      CHECK_EQ(error[0], '\0');
    }
    chronos_session_free(session);

    const chronos::Image result = chronos_test::read_image_or_fail(output, false);
    CHECK_EQ(result.width, source.width * scale);
    CHECK_EQ(result.height, source.height * scale);
    CHECK_EQ(result.pixels.size(), static_cast<size_t>(source.width * scale) *
                                       static_cast<size_t>(source.height * scale) * 3);
    /* Both test images are constant, so seam-free blending must keep them exact. */
    for (size_t i = 0; i < result.pixels.size(); i += 3) {
      CHECK_EQ(result.pixels[i + 0], source.pixels[0]);
      CHECK_EQ(result.pixels[i + 1], source.pixels[1]);
      CHECK_EQ(result.pixels[i + 2], source.pixels[2]);
    }
  });
}

}  // namespace

TEST(threads, two_threads_two_independent_sessions) {
  const chronos::Image first_source = chronos_test::make_constant_rgb(28, 20, 200, 30, 40);
  const chronos::Image second_source = chronos_test::make_constant_rgb(36, 18, 10, 220, 90);
  const std::string first_input = chronos_test::temp_path("threads_a_in.png");
  const std::string second_input = chronos_test::temp_path("threads_b_in.png");
  const std::string first_output = chronos_test::temp_path("threads_a_out.png");
  const std::string second_output = chronos_test::temp_path("threads_b_out.png");
  chronos_test::write_image_or_fail(first_input, first_source);
  chronos_test::write_image_or_fail(second_input, second_source);

  ThreadOutcome first_outcome;
  ThreadOutcome second_outcome;
  std::thread first_thread(process_in_thread, std::cref(first_source), std::cref(first_input),
                           std::cref(first_output), 2, 16, 4, &first_outcome);
  std::thread second_thread(process_in_thread, std::cref(second_source), std::cref(second_input),
                            std::cref(second_output), 3, 24, 6, &second_outcome);
  first_thread.join();
  second_thread.join();

  if (!first_outcome.ok) ::chronos_test::report_failure(__FILE__, __LINE__, first_outcome.message);
  if (!second_outcome.ok) ::chronos_test::report_failure(__FILE__, __LINE__, second_outcome.message);

  const chronos::Image first_result = chronos_test::read_image_or_fail(first_output, false);
  const chronos::Image second_result = chronos_test::read_image_or_fail(second_output, false);
  CHECK_EQ(first_result.width, 56);
  CHECK_EQ(first_result.height, 40);
  CHECK_EQ(second_result.width, 108);
  CHECK_EQ(second_result.height, 54);
  CHECK_EQ(first_result.pixels[0], 200);
  CHECK_EQ(second_result.pixels[0], 10);

  chronos_test::remove_file(first_input);
  chronos_test::remove_file(second_input);
  chronos_test::remove_file(first_output);
  chronos_test::remove_file(second_output);
}

TEST(threads, last_error_is_thread_local) {
  std::atomic<bool> failure_recorded{false};
  std::atomic<bool> other_thread_checked{false};
  std::atomic<int> first_failed{0};
  std::atomic<int> second_failed{0};
  std::string first_message;
  std::string second_message;

  ThreadOutcome first_outcome;
  ThreadOutcome second_outcome;

  std::thread first_thread([&]() {
    run_guarded(&first_outcome, [&]() {
      chronos_options bad;
      std::memset(&bad, 0, sizeof(bad));
      bad.struct_size = 1; /* invalid on purpose */
      char error[256];
      error[0] = '\0';
      CHECK(chronos_session_create(nullptr, 2, &bad, error, static_cast<int>(sizeof(error))) ==
            nullptr);
      CHECK(std::strlen(chronos_last_error()) > 0);

      failure_recorded.store(true);
      /* Give the other thread plenty of time to read its own last_error. */
      const auto deadline = std::chrono::steady_clock::now() + std::chrono::seconds(5);
      while (!other_thread_checked.load() && std::chrono::steady_clock::now() < deadline) {
        std::this_thread::sleep_for(std::chrono::milliseconds(1));
      }
      /* Our own error must still be there (no other thread overwrote it). */
      CHECK(std::strlen(chronos_last_error()) > 0);
    });
  });

  std::thread second_thread([&]() {
    run_guarded(&second_outcome, [&]() {
      const auto deadline = std::chrono::steady_clock::now() + std::chrono::seconds(5);
      while (!failure_recorded.load() && std::chrono::steady_clock::now() < deadline) {
        std::this_thread::sleep_for(std::chrono::milliseconds(1));
      }
      /* This thread never failed: its last_error must be empty. */
      CHECK_EQ(std::strlen(chronos_last_error()), static_cast<size_t>(0));
      other_thread_checked.store(true);
    });
  });

  first_thread.join();
  second_thread.join();
  (void)first_failed;
  (void)second_failed;
  (void)first_message;
  (void)second_message;

  if (!first_outcome.ok) ::chronos_test::report_failure(__FILE__, __LINE__, first_outcome.message);
  if (!second_outcome.ok) ::chronos_test::report_failure(__FILE__, __LINE__, second_outcome.message);
}
