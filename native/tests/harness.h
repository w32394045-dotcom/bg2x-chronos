/*
 * Project Chronos — tiny dependency-free test harness.
 *
 * Deliberately minimal: a self-registering registry, CHECK macros that throw on
 * the first failure, and a main() that runs one suite (selected with
 * `--suite <name>`) or everything. Each suite is registered as its own ctest
 * entry, so a failure points at a small group of tests.
 */
#ifndef CHRONOS_TEST_HARNESS_H
#define CHRONOS_TEST_HARNESS_H

#include <cmath>
#include <functional>
#include <string>
#include <vector>

namespace chronos_test {

struct TestCase {
  std::string suite;
  std::string name;
  std::function<void()> fn;
};

/* Function-local static in test_main.cpp: no cross-TU initialisation order issues. */
std::vector<TestCase>& registry();

/* Throws std::runtime_error; the runner reports the failure and continues. */
void report_failure(const char* file, int line, const std::string& message);

/*
 * Thrown by SKIP_TEST(). The runner reports the test as skipped instead of
 * failed and exits with 77 when every selected test was skipped, so ctest can
 * mark the suite "Skipped" (SKIP_RETURN_CODE).
 */
struct SkipTest {
  std::string message;
};

[[noreturn]] inline void report_skip(const char* file, int line, const std::string& message) {
  std::string location = file != nullptr ? std::string(file) : std::string("?");
  location += ":";
  location += std::to_string(line);
  location += ": ";
  location += message;
  throw SkipTest{location};
}

struct Registrar {
  Registrar(const char* suite, const char* name, std::function<void()> fn);
};

template <typename T>
std::string value_to_string(const T& value);

template <>
inline std::string value_to_string<std::string>(const std::string& value) {
  return "\"" + value + "\"";
}

inline std::string value_to_string(const char* value) {
  return std::string("\"") + (value != nullptr ? value : "(null)") + "\"";
}

template <typename T>
std::string value_to_string(const T& value) {
  return std::to_string(value);
}

template <typename A, typename B>
void check_equal(const A& actual, const B& expected, const char* file, int line,
                 const char* expression) {
  if (!(actual == expected)) {
    report_failure(file, line,
                   std::string("CHECK_EQ failed: ") + expression + " (actual=" +
                       value_to_string(actual) + ", expected=" + value_to_string(expected) + ")");
  }
}

template <typename A, typename B>
void check_near(const A& actual, const B& expected, double epsilon, const char* file, int line,
                const char* expression) {
  const double difference =
      std::fabs(static_cast<double>(actual) - static_cast<double>(expected));
  if (!(difference <= epsilon)) {
    report_failure(file, line,
                   std::string("CHECK_NEAR failed: ") + expression + " (actual=" +
                       value_to_string(actual) + ", expected=" + value_to_string(expected) +
                       ", delta=" + value_to_string(difference) +
                       ", epsilon=" + value_to_string(epsilon) + ")");
  }
}

}  // namespace chronos_test

#define CHRONOS_TEST_CONCAT_INNER(a, b) a##b
#define CHRONOS_TEST_CONCAT(a, b) CHRONOS_TEST_CONCAT_INNER(a, b)

#define TEST(suite_name, test_name)                                                       \
  static void CHRONOS_TEST_CONCAT(CHRONOS_TEST_CASE_, __LINE__)();                        \
  static ::chronos_test::Registrar CHRONOS_TEST_CONCAT(CHRONOS_TEST_REG_, __LINE__)(      \
      #suite_name, #test_name, &CHRONOS_TEST_CONCAT(CHRONOS_TEST_CASE_, __LINE__));       \
  static void CHRONOS_TEST_CONCAT(CHRONOS_TEST_CASE_, __LINE__)()

#define CHECK(condition)                                                            \
  do {                                                                              \
    if (!(condition)) {                                                             \
      ::chronos_test::report_failure(__FILE__, __LINE__, "CHECK failed: " #condition); \
    }                                                                               \
  } while (false)

#define CHECK_EQ(actual, expected) \
  ::chronos_test::check_equal((actual), (expected), __FILE__, __LINE__, #actual " == " #expected)

#define CHECK_NEAR(actual, expected, epsilon)                                     \
  ::chronos_test::check_near((actual), (expected), (epsilon), __FILE__, __LINE__, \
                             #actual " ~= " #expected)

#define CHECK_TRUE(condition) CHECK(condition)
#define CHECK_FALSE(condition) CHECK(!(condition))

/* Marks a test as skipped (not failed) with a human readable reason. */
#define SKIP_TEST(message) ::chronos_test::report_skip(__FILE__, __LINE__, (message))

#endif /* CHRONOS_TEST_HARNESS_H */
