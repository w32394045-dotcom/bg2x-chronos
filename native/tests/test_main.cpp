/*
 * Project Chronos — test runner.
 */
#include <cstdio>
#include <cstring>
#include <exception>
#include <stdexcept>
#include <string>
#include <vector>

#include "harness.h"

namespace chronos_test {

std::vector<TestCase>& registry() {
  static std::vector<TestCase> tests;
  return tests;
}

void report_failure(const char* file, int line, const std::string& message) {
  char location[512];
  std::snprintf(location, sizeof(location), "%s:%d: %s", file != nullptr ? file : "?",
                line, message.c_str());
  throw std::runtime_error(location);
}

Registrar::Registrar(const char* suite, const char* name, std::function<void()> fn) {
  TestCase test;
  test.suite = suite != nullptr ? suite : "";
  test.name = name != nullptr ? name : "";
  test.fn = std::move(fn);
  registry().push_back(std::move(test));
}

}  // namespace chronos_test

namespace {

void print_usage(const char* program) {
  std::printf("usage: %s [--suite <name>] [--list]\n", program);
}

}  // namespace

int main(int argc, char** argv) {
  std::string suite_filter;
  bool list_only = false;
  for (int i = 1; i < argc; ++i) {
    const std::string argument = argv[i];
    if (argument == "--suite") {
      if (i + 1 >= argc) {
        print_usage(argv[0]);
        return 2;
      }
      suite_filter = argv[++i];
    } else if (argument == "--list") {
      list_only = true;
    } else if (argument == "--help" || argument == "-h") {
      print_usage(argv[0]);
      return 0;
    } else {
      std::fprintf(stderr, "unknown argument: %s\n", argument.c_str());
      print_usage(argv[0]);
      return 2;
    }
  }

  const std::vector<chronos_test::TestCase>& tests = chronos_test::registry();

  if (list_only) {
    for (const chronos_test::TestCase& test : tests) {
      std::printf("%s.%s\n", test.suite.c_str(), test.name.c_str());
    }
    return 0;
  }

  int passed = 0;
  int failed = 0;
  for (const chronos_test::TestCase& test : tests) {
    if (!suite_filter.empty() && test.suite != suite_filter) continue;
    std::printf("[ RUN      ] %s.%s\n", test.suite.c_str(), test.name.c_str());
    std::fflush(stdout);
    try {
      test.fn();
      std::printf("[       OK ] %s.%s\n", test.suite.c_str(), test.name.c_str());
      ++passed;
    } catch (const std::exception& exception) {
      std::printf("[  FAILED  ] %s.%s\n            %s\n", test.suite.c_str(), test.name.c_str(),
                  exception.what());
      ++failed;
    } catch (...) {
      std::printf("[  FAILED  ] %s.%s\n            unknown exception\n", test.suite.c_str(),
                  test.name.c_str());
      ++failed;
    }
    std::fflush(stdout);
  }

  if (passed == 0 && failed == 0) {
    std::fprintf(stderr, "no tests matched suite '%s'\n", suite_filter.c_str());
    return 1;
  }

  std::printf("------------------------------------------------\n");
  std::printf("[  PASSED  ] %d test(s)\n", passed);
  if (failed > 0) {
    std::printf("[  FAILED  ] %d test(s)\n", failed);
    return 1;
  }
  return 0;
}
