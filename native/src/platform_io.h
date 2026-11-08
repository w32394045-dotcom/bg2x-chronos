/*
 * Project Chronos - UTF-8 aware path/file helpers.
 *
 * Windows narrow file APIs use the ANSI code page, which corrupts paths that
 * contain non-ASCII characters (the project itself lives below a directory with
 * a CJK name). Every caller supplied path opened by the native layer goes
 * through these helpers; internal data stays UTF-8.
 */
#ifndef CHRONOS_PLATFORM_IO_H
#define CHRONOS_PLATFORM_IO_H

#include <cstdio>
#include <string>

namespace chronos {

/* Opens path (UTF-8) with the given fopen mode ("rb", "wb", ...). NULL on failure. */
std::FILE* fopen_utf8(const std::string& path, const char* mode);

/* True when path exists (files and directories). */
bool path_exists(const std::string& path);

/* Directory part of path ("" when there is none). */
std::string directory_of(const std::string& path);

/* Joins with the platform separator; returns leaf when dir is empty. */
std::string join_path(const std::string& dir, const std::string& leaf);

/* Creates a single directory (not recursive). False when it already exists. */
bool create_directory_utf8(const std::string& path);

/* Removes one file / one empty directory. False when it does not exist. */
bool remove_file_utf8(const std::string& path);
bool remove_directory_utf8(const std::string& path);

/* Directory containing the running executable ("" when it cannot be determined). */
std::string executable_directory();

/* Reads an environment variable as UTF-8 ("" when unset or empty). */
std::string getenv_utf8(const char* name);

/*
 * Dynamic library helpers used by the ONNX Runtime loader. load_shared_library
 * accepts either a full/relative path or a bare library file name; symbols are
 * looked up by their C name. Returns NULL on failure.
 */
void* load_shared_library(const std::string& path_or_name);
void* shared_library_symbol(void* handle, const char* name);
/* Releases a handle returned by load_shared_library (safe with NULL). */
void close_shared_library(void* handle);

}  // namespace chronos

#endif /* CHRONOS_PLATFORM_IO_H */
