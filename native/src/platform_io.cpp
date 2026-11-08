/*
 * Project Chronos - UTF-8 aware path/file helpers (implementation).
 *
 * On Windows every path is converted to UTF-16 and passed to the wide CRT /
 * Win32 API; on POSIX the UTF-8 bytes go straight to the libc calls.
 */
#include "platform_io.h"

#include <climits>
#include <cstring>
#include <string>

#if defined(_WIN32)
#define WIN32_LEAN_AND_MEAN
#include <windows.h>

#include <direct.h>
#include <sys/stat.h>
#else
#include <dlfcn.h>
#include <sys/stat.h>
#include <unistd.h>

#include <cstdlib>
#endif

namespace chronos {
namespace {

#if defined(_WIN32)

std::wstring wide_from_utf8(const std::string& text) {
  if (text.empty()) return std::wstring();
  if (text.size() > static_cast<size_t>(INT_MAX)) return std::wstring();
  const int length = static_cast<int>(text.size());
  const int needed =
      ::MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, text.data(), length, nullptr, 0);
  if (needed <= 0) return std::wstring();
  std::wstring result(static_cast<size_t>(needed), L'\0');
  if (::MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, text.data(), length, &result[0],
                            needed) <= 0) {
    return std::wstring();
  }
  return result;
}

std::string utf8_from_wide(const wchar_t* text, int length) {
  if (text == nullptr || length <= 0) return std::string();
  const int needed = ::WideCharToMultiByte(CP_UTF8, 0, text, length, nullptr, 0, nullptr, nullptr);
  if (needed <= 0) return std::string();
  std::string result(static_cast<size_t>(needed), '\0');
  ::WideCharToMultiByte(CP_UTF8, 0, text, length, &result[0], needed, nullptr, nullptr);
  return result;
}

std::wstring environment_variable_wide(const char* name) {
  const std::wstring wide_name = wide_from_utf8(name != nullptr ? name : "");
  if (wide_name.empty()) return std::wstring();
  std::wstring buffer(256, L'\0');
  for (int attempt = 0; attempt < 8; ++attempt) {
    const DWORD length =
        ::GetEnvironmentVariableW(wide_name.c_str(), &buffer[0], static_cast<DWORD>(buffer.size()));
    if (length == 0) return std::wstring();
    if (length < static_cast<DWORD>(buffer.size())) {
      buffer.resize(static_cast<size_t>(length));
      return buffer;
    }
    buffer.resize(static_cast<size_t>(length) + 1);
  }
  return std::wstring();
}

bool has_path_separator(const std::string& path) {
  return path.find_first_of("\\/") != std::string::npos;
}

#else

bool has_path_separator(const std::string& path) {
  return path.find('/') != std::string::npos;
}

#endif

const char kNativeSeparator =
#if defined(_WIN32)
    '\\';
#else
    '/';
#endif

}  // namespace

std::FILE* fopen_utf8(const std::string& path, const char* mode) {
  if (path.empty() || mode == nullptr) return nullptr;
#if defined(_WIN32)
  const std::wstring wide_path = wide_from_utf8(path);
  std::wstring wide_mode;
  for (const char* c = mode; c != nullptr && *c != '\0'; ++c) {
    wide_mode.push_back(static_cast<wchar_t>(static_cast<unsigned char>(*c)));
  }
  if (wide_path.empty() || wide_mode.empty()) return nullptr;
  return ::_wfopen(wide_path.c_str(), wide_mode.c_str());
#else
  return std::fopen(path.c_str(), mode);
#endif
}

bool path_exists(const std::string& path) {
  if (path.empty()) return false;
#if defined(_WIN32)
  const std::wstring wide_path = wide_from_utf8(path);
  if (wide_path.empty()) return false;
  struct _stat64 info;
  std::memset(&info, 0, sizeof(info));
  return ::_wstat64(wide_path.c_str(), &info) == 0;
#else
  struct stat info;
  std::memset(&info, 0, sizeof(info));
  return ::stat(path.c_str(), &info) == 0;
#endif
}

std::string directory_of(const std::string& path) {
  const size_t separator = path.find_last_of("\\/");
  if (separator == std::string::npos) return std::string();
#if defined(_WIN32)
  /* Keep the drive root intact: "C:\model.onnx" -> "C:\". */
  if (separator == 2 && path.size() > 2 && path[1] == ':') return path.substr(0, 3);
#endif
  if (separator == 0) return path.substr(0, 1);
  return path.substr(0, separator);
}

std::string join_path(const std::string& dir, const std::string& leaf) {
  if (dir.empty()) return leaf;
  if (leaf.empty()) return dir;
  const bool dir_ends_with_separator =
      dir[dir.size() - 1] == '/' || dir[dir.size() - 1] == '\\';
  if (dir_ends_with_separator) return dir + leaf;
  return dir + kNativeSeparator + leaf;
}

bool create_directory_utf8(const std::string& path) {
  if (path.empty()) return false;
#if defined(_WIN32)
  const std::wstring wide_path = wide_from_utf8(path);
  if (wide_path.empty()) return false;
  return ::_wmkdir(wide_path.c_str()) == 0;
#else
  return ::mkdir(path.c_str(), 0777) == 0;
#endif
}

bool remove_file_utf8(const std::string& path) {
  if (path.empty()) return false;
#if defined(_WIN32)
  const std::wstring wide_path = wide_from_utf8(path);
  if (wide_path.empty()) return false;
  return ::_wremove(wide_path.c_str()) == 0;
#else
  return ::remove(path.c_str()) == 0;
#endif
}

bool remove_directory_utf8(const std::string& path) {
  if (path.empty()) return false;
#if defined(_WIN32)
  const std::wstring wide_path = wide_from_utf8(path);
  if (wide_path.empty()) return false;
  return ::_wrmdir(wide_path.c_str()) == 0;
#else
  return ::rmdir(path.c_str()) == 0;
#endif
}

std::string executable_directory() {
#if defined(_WIN32)
  std::wstring buffer(MAX_PATH, L'\0');
  for (int attempt = 0; attempt < 8; ++attempt) {
    const DWORD length = ::GetModuleFileNameW(nullptr, &buffer[0],
                                              static_cast<DWORD>(buffer.size()));
    if (length == 0) return std::string();
    if (length < static_cast<DWORD>(buffer.size())) {
      buffer.resize(static_cast<size_t>(length));
      break;
    }
    buffer.resize(buffer.size() * 2);
  }
  if (buffer.empty()) return std::string();
  return directory_of(utf8_from_wide(buffer.c_str(), static_cast<int>(buffer.size())));
#else
  std::string buffer(4096, '\0');
  const ssize_t length = ::readlink("/proc/self/exe", &buffer[0], buffer.size() - 1);
  if (length <= 0) return std::string();
  buffer.resize(static_cast<size_t>(length));
  return directory_of(buffer);
#endif
}

std::string getenv_utf8(const char* name) {
  if (name == nullptr || *name == '\0') return std::string();
#if defined(_WIN32)
  const std::wstring wide = environment_variable_wide(name);
  if (wide.empty()) return std::string();
  return utf8_from_wide(wide.c_str(), static_cast<int>(wide.size()));
#else
  const char* value = std::getenv(name);
  return value != nullptr ? std::string(value) : std::string();
#endif
}

void* load_shared_library(const std::string& path_or_name) {
  if (path_or_name.empty()) return nullptr;
#if defined(_WIN32)
  const std::wstring wide = wide_from_utf8(path_or_name);
  if (wide.empty()) return nullptr;
  if (!has_path_separator(path_or_name)) {
    return reinterpret_cast<void*>(::LoadLibraryW(wide.c_str()));
  }
  /* Full path: resolve dependent DLLs next to the library (the official ORT
   * zip keeps onnxruntime_providers_shared.dll beside onnxruntime.dll). */
  return reinterpret_cast<void*>(
      ::LoadLibraryExW(wide.c_str(), nullptr, LOAD_WITH_ALTERED_SEARCH_PATH));
#else
  return ::dlopen(path_or_name.c_str(), RTLD_NOW | RTLD_LOCAL);
#endif
}

void* shared_library_symbol(void* handle, const char* name) {
  if (handle == nullptr || name == nullptr || *name == '\0') return nullptr;
#if defined(_WIN32)
  return reinterpret_cast<void*>(
      ::GetProcAddress(reinterpret_cast<HMODULE>(handle), name));
#else
  return ::dlsym(handle, name);
#endif
}

void close_shared_library(void* handle) {
  if (handle == nullptr) return;
#if defined(_WIN32)
  ::FreeLibrary(reinterpret_cast<HMODULE>(handle));
#else
  ::dlclose(handle);
#endif
}

}  // namespace chronos
