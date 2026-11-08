/*
 * Project Chronos — native CLI (phase 1).
 *
 * A thin diagnostic front-end over the frozen C ABI. It exists so the native
 * library can be exercised by hand on Windows/Linux without the Go layer:
 *
 *   chronos_native_cli --in a.png --out b.png [--model m.xml] [--scale 4]
 *                      [--tile 256] [--overlap 16] [--threads N] [--fast]
 *                      [--progress] [--info] [--help] [--version]
 *
 * Output is one JSON-ish line per event (easy to grep in CI logs). The exit
 * code is the C API return code (0 = success, negative mapped to 8-bit).
 */
#include <chronos/chronos.h>

#include <atomic>
#include <chrono>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cwchar>
#include <string>
#include <thread>
#include <vector>

#if defined(_WIN32)
/* Windows only: recover the UTF-16 command line and convert it to UTF-8 so
 * paths with non-ASCII characters (e.g. Chinese folder names) survive. The
 * MinGW CRT would otherwise narrow argv using the ANSI code page. This is the
 * only place in the native tree that touches windows.h; the library itself
 * stays fully portable. */
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <shellapi.h>
#endif

namespace {

struct CliArgs {
  std::string in_path;
  std::string out_path;
  std::string model_path;
  int scale = 4;
  int tile = 256;
  int overlap = 16;
  int threads = 0;
  bool fast = false;
  bool info = false;
  bool progress = false;
  bool help = false;
  bool version = false;
};

#if defined(_WIN32)
std::string utf8_from_wide(const wchar_t* text) {
  if (text == nullptr || *text == L'\0') return std::string();
  const int length = static_cast<int>(std::wcslen(text));
  const int needed =
      ::WideCharToMultiByte(CP_UTF8, 0, text, length, nullptr, 0, nullptr, nullptr);
  if (needed <= 0) return std::string();
  std::string result(static_cast<size_t>(needed), '\0');
  ::WideCharToMultiByte(CP_UTF8, 0, text, length, &result[0], needed, nullptr, nullptr);
  return result;
}

std::vector<std::string> collect_arguments(int argc, char** argv) {
  (void)argc;
  (void)argv;
  std::vector<std::string> arguments;
  int count = 0;
  wchar_t** wide = ::CommandLineToArgvW(::GetCommandLineW(), &count);
  if (wide == nullptr) return arguments;
  arguments.reserve(static_cast<size_t>(count));
  for (int i = 0; i < count; ++i) arguments.push_back(utf8_from_wide(wide[i]));
  ::LocalFree(wide);
  return arguments;
}
#else
std::vector<std::string> collect_arguments(int argc, char** argv) {
  return std::vector<std::string>(argv, argv + argc);
}
#endif

std::string json_escape(const std::string& value) {
  std::string escaped;
  escaped.reserve(value.size() + 8);
  for (char c : value) {
    switch (c) {
      case '"':
        escaped += "\\\"";
        break;
      case '\\':
        escaped += "\\\\";
        break;
      case '\n':
        escaped += "\\n";
        break;
      case '\r':
        escaped += "\\r";
        break;
      case '\t':
        escaped += "\\t";
        break;
      default:
        if (static_cast<unsigned char>(c) < 0x20) {
          char buffer[8];
          std::snprintf(buffer, sizeof(buffer), "\\u%04x", static_cast<unsigned>(c));
          escaped += buffer;
        } else {
          escaped += c;
        }
        break;
    }
  }
  return escaped;
}

void print_usage() {
  std::printf(
      "chronos_native_cli — Project Chronos native compute layer\n"
      "\n"
      "usage: chronos_native_cli --in <input> --out <output> [options]\n"
      "\n"
      "  --in <path>        input image (png/jpg/bmp)\n"
      "  --out <path>       output image; format chosen by extension\n"
      "  --model <path>     OpenVINO IR (.xml) or ONNX (.onnx); omit for stub\n"
      "  --scale <n>        output scale, default 4 (stub supports 1..4)\n"
      "  --tile <n>         tile size, default 256\n"
      "  --overlap <n>      tile overlap, default 16\n"
      "  --threads <n>      worker threads, default hardware (capped at 8)\n"
      "  --fast             bilinear resample instead of Lanczos3 (stub)\n"
      "  --progress         poll progress every 100 ms while processing\n"
      "  --info             print build/backend info as JSON and exit\n"
      "  --help             show this help\n"
      "  --version          print version string\n");
}

bool parse_int(const char* text, int* value) {
  if (text == nullptr || *text == '\0') return false;
  char* end = nullptr;
  const long parsed = std::strtol(text, &end, 10);
  if (end == nullptr || *end != '\0') return false;
  *value = static_cast<int>(parsed);
  return true;
}

bool parse_args(const std::vector<std::string>& argv, CliArgs* args, std::string* error) {
  for (size_t i = 1; i < argv.size(); ++i) {
    const std::string& argument = argv[i];
    const bool wants_value = argument == "--in" || argument == "--out" ||
                             argument == "--model" || argument == "--scale" ||
                             argument == "--tile" || argument == "--overlap" ||
                             argument == "--threads";
    if (wants_value) {
      if (i + 1 >= argv.size()) {
        *error = "missing value for " + argument;
        return false;
      }
      const std::string& value = argv[++i];
      if (argument == "--in") {
        args->in_path = value;
      } else if (argument == "--out") {
        args->out_path = value;
      } else if (argument == "--model") {
        args->model_path = value;
      } else {
        int parsed = 0;
        if (!parse_int(value.c_str(), &parsed)) {
          *error = "invalid integer for " + argument + ": " + value;
          return false;
        }
        if (argument == "--scale") {
          args->scale = parsed;
        } else if (argument == "--tile") {
          args->tile = parsed;
        } else if (argument == "--overlap") {
          args->overlap = parsed;
        } else {
          args->threads = parsed;
        }
      }
      continue;
    }
    if (argument == "--fast") {
      args->fast = true;
    } else if (argument == "--info") {
      args->info = true;
    } else if (argument == "--progress") {
      args->progress = true;
    } else if (argument == "--help" || argument == "-h") {
      args->help = true;
    } else if (argument == "--version") {
      args->version = true;
    } else {
      *error = "unknown argument: " + argument;
      return false;
    }
  }
  return true;
}

void print_info() {
  std::printf(
      "{\"event\":\"info\",\"abi\":%d,\"version\":\"%s\",\"backends\":{\"stub\":%s,"
      "\"openvino\":%s}}\n",
      chronos_abi_version(), json_escape(chronos_version_string()).c_str(),
      chronos_backend_available(CHRONOS_BACKEND_STUB) ? "true" : "false",
      chronos_backend_available(CHRONOS_BACKEND_OPENVINO) ? "true" : "false");
}

}  // namespace

int main(int argc, char** argv) {
  const std::vector<std::string> arguments = collect_arguments(argc, argv);
  CliArgs args;
  std::string parse_error;
  if (!parse_args(arguments, &args, &parse_error)) {
    std::fprintf(stderr, "{\"event\":\"error\",\"code\":%d,\"message\":\"%s\"}\n",
                 CHRONOS_ERR_PARAM, json_escape(parse_error).c_str());
    print_usage();
    return 2;
  }
  if (args.help) {
    print_usage();
    return 0;
  }
  if (args.version) {
    std::printf("%s\n", chronos_version_string());
    return 0;
  }
  if (args.info) print_info();
  if (args.in_path.empty() || args.out_path.empty()) {
    if (!args.info) {
      std::fprintf(stderr,
                   "{\"event\":\"error\",\"code\":%d,\"message\":\"--in and --out are "
                   "required\"}\n",
                   CHRONOS_ERR_PARAM);
      print_usage();
      return 2;
    }
    return 0;
  }

  chronos_options options;
  std::memset(&options, 0, sizeof(options));
  options.struct_size = static_cast<int>(sizeof(options));
  options.tile_size = args.tile;
  options.overlap = args.overlap;
  options.threads = args.threads;
  options.device_index = 0;
  options.flags = args.fast ? CHRONOS_FLAG_FAST : CHRONOS_FLAG_NONE;

  char error[1024];
  error[0] = '\0';
  chronos_session* session = chronos_session_create(
      args.model_path.empty() ? nullptr : args.model_path.c_str(), args.scale, &options, error,
      static_cast<int>(sizeof(error)));
  if (session == nullptr) {
    std::fprintf(stderr, "{\"event\":\"error\",\"code\":%d,\"message\":\"%s\"}\n",
                 CHRONOS_ERR_BACKEND, json_escape(error).c_str());
    return 1;
  }

  char description[256];
  description[0] = '\0';
  chronos_session_describe(session, description, static_cast<int>(sizeof(description)));
  std::printf(
      "{\"event\":\"session\",\"backend\":\"%s\",\"scale\":%d,\"tile\":%d,\"overlap\":%d,"
      "\"threads\":%d,\"fast\":%s}\n",
      json_escape(description).c_str(), chronos_session_scale(session), args.tile, args.overlap,
      args.threads, args.fast ? "true" : "false");
  std::fflush(stdout);

  std::atomic<bool> finished{false};
  int reported_done = -1;
  int reported_total = -1;
  std::thread progress_thread;
  if (args.progress) {
    progress_thread = std::thread([&session, &finished, &reported_done, &reported_total]() {
      while (!finished.load(std::memory_order_relaxed)) {
        int done = 0;
        int total = 0;
        chronos_session_progress(session, &done, &total);
        if (done != reported_done || total != reported_total) {
          reported_done = done;
          reported_total = total;
          std::printf("{\"event\":\"progress\",\"done\":%d,\"total\":%d}\n", done, total);
          std::fflush(stdout);
        }
        std::this_thread::sleep_for(std::chrono::milliseconds(100));
      }
    });
  }

  const auto start = std::chrono::steady_clock::now();
  int width = 0;
  int height = 0;
  error[0] = '\0';
  const int rc = chronos_process(session, args.in_path.c_str(), args.out_path.c_str(), &width,
                                 &height, error, static_cast<int>(sizeof(error)));
  const auto elapsed =
      std::chrono::duration_cast<std::chrono::milliseconds>(std::chrono::steady_clock::now() -
                                                            start);

  finished.store(true, std::memory_order_relaxed);
  if (progress_thread.joinable()) progress_thread.join();

  int done = 0;
  int total = 0;
  chronos_session_progress(session, &done, &total);

  if (rc != CHRONOS_OK) {
    std::fprintf(stderr,
                 "{\"event\":\"error\",\"code\":%d,\"message\":\"%s\",\"elapsed_ms\":%lld}\n",
                 rc, json_escape(error).c_str(), static_cast<long long>(elapsed.count()));
    chronos_session_free(session);
    return rc;
  }

  std::printf(
      "{\"event\":\"done\",\"in\":\"%s\",\"out\":\"%s\",\"width\":%d,\"height\":%d,"
      "\"tiles_done\":%d,\"tiles_total\":%d,\"elapsed_ms\":%lld}\n",
      json_escape(args.in_path).c_str(), json_escape(args.out_path).c_str(), width, height, done,
      total, static_cast<long long>(elapsed.count()));
  chronos_session_free(session);
  return 0;
}
