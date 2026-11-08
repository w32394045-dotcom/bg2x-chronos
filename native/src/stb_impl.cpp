/*
 * Project Chronos — stb implementation translation unit.
 *
 * The vendored stb headers live in native/third_party. They are compiled in
 * exactly this one TU (with third-party warnings disabled by CMake) while the
 * rest of the code only sees their declarations.
 *
 * STBI_WINDOWS_UTF8 / STBIW_WINDOWS_UTF8 make stb convert UTF-8 paths to wide
 * characters on Windows: the Go layer passes UTF-8 file paths, which may
 * contain non-ASCII characters (e.g. Chinese folder names).
 */
#if defined(_WIN32)
#define STBI_WINDOWS_UTF8
#define STBIW_WINDOWS_UTF8
#endif

#define STB_IMAGE_IMPLEMENTATION
#include "stb_image.h"

#define STB_IMAGE_WRITE_IMPLEMENTATION
#include "stb_image_write.h"
