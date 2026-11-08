/*
 * Project Chronos — shared internal helper header.
 *
 * Kept private (src/): none of this is part of the installed headers.
 */
#ifndef CHRONOS_COMMON_H
#define CHRONOS_COMMON_H

#include <algorithm>
#include <cstddef>
#include <cstdint>
#include <cstdio>
#include <cstring>
#include <string>

namespace chronos {

/* NUL-terminated copy into a caller buffer; truncates but always terminates. */
inline void copy_error(char* err, int err_len, const std::string& message) {
  if (err == nullptr || err_len <= 0) return;
  const size_t capacity = static_cast<size_t>(err_len);
  const size_t count = message.size() < capacity - 1 ? message.size() : capacity - 1;
  if (count > 0) std::memcpy(err, message.data(), count);
  err[count] = '\0';
}

inline void set_error(std::string* err, const char* message) {
  if (err != nullptr) *err = message;
}

inline void set_error(std::string* err, const std::string& message) {
  if (err != nullptr) *err = message;
}

template <typename T>
inline T clamp_value(T value, T low, T high) {
  return value < low ? low : (value > high ? high : value);
}

inline uint8_t to_u8(float value) {
  if (value <= 0.0f) return 0;
  if (value >= 255.0f) return 255;
  return static_cast<uint8_t>(value + 0.5f);
}

}  // namespace chronos

#endif /* CHRONOS_COMMON_H */
