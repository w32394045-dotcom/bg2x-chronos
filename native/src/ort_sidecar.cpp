/*
 * Project Chronos - ONNX model sidecar parser (implementation).
 *
 * Dependency free, defensive JSON reader: bounded nesting depth, bounded file
 * size, no exceptions used for control flow. A malformed sidecar is reported as
 * a clear error instead of being silently ignored.
 */
#include "ort_sidecar.h"

#include <cmath>
#include <cstddef>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <limits>
#include <string>
#include <utility>
#include <vector>

#include "platform_io.h"

namespace chronos {
namespace {

constexpr size_t kMaxSidecarBytes = 1024 * 1024;
constexpr int kMaxJsonDepth = 32;

struct JsonValue {
  enum class Kind { kNull, kBool, kNumber, kString, kArray, kObject };
  Kind kind = Kind::kNull;
  bool boolean = false;
  double number = 0.0;
  std::string text;
  std::vector<JsonValue> elements;
  std::vector<std::pair<std::string, JsonValue>> members;

  const JsonValue* find(const std::string& key) const {
    if (kind != Kind::kObject) return nullptr;
    for (const auto& member : members) {
      if (member.first == key) return &member.second;
    }
    return nullptr;
  }
};

void append_utf8(std::string* output, unsigned long codepoint) {
  if (codepoint <= 0x7F) {
    output->push_back(static_cast<char>(codepoint));
  } else if (codepoint <= 0x7FF) {
    output->push_back(static_cast<char>(0xC0 | (codepoint >> 6)));
    output->push_back(static_cast<char>(0x80 | (codepoint & 0x3F)));
  } else if (codepoint <= 0xFFFF) {
    output->push_back(static_cast<char>(0xE0 | (codepoint >> 12)));
    output->push_back(static_cast<char>(0x80 | ((codepoint >> 6) & 0x3F)));
    output->push_back(static_cast<char>(0x80 | (codepoint & 0x3F)));
  } else {
    output->push_back(static_cast<char>(0xF0 | (codepoint >> 18)));
    output->push_back(static_cast<char>(0x80 | ((codepoint >> 12) & 0x3F)));
    output->push_back(static_cast<char>(0x80 | ((codepoint >> 6) & 0x3F)));
    output->push_back(static_cast<char>(0x80 | (codepoint & 0x3F)));
  }
}

int hex_value(char c) {
  if (c >= '0' && c <= '9') return c - '0';
  if (c >= 'a' && c <= 'f') return c - 'a' + 10;
  if (c >= 'A' && c <= 'F') return c - 'A' + 10;
  return -1;
}

class JsonParser {
 public:
  explicit JsonParser(const std::string& text) : text_(text) {}

  bool parse(JsonValue* output, std::string* error) {
    skip_whitespace();
    if (!parse_value(output, 0)) return fail(error);
    skip_whitespace();
    if (position_ != text_.size()) {
      error_ = "trailing characters";
      return fail(error);
    }
    return true;
  }

 private:
  void skip_whitespace() {
    while (position_ < text_.size()) {
      const char c = text_[position_];
      if (c != ' ' && c != '\t' && c != '\n' && c != '\r') break;
      ++position_;
    }
  }

  bool fail(std::string* error) {
    *error = "model.json: parse error at offset " + std::to_string(position_) + ": " + error_;
    return false;
  }

  bool set_error(const char* message) {
    error_ = message;
    return false;
  }

  bool expect(char character) {
    skip_whitespace();
    if (position_ >= text_.size() || text_[position_] != character) {
      error_ = std::string("expected '") + character + "'";
      return false;
    }
    ++position_;
    return true;
  }

  bool parse_value(JsonValue* output, int depth) {
    if (depth > kMaxJsonDepth) return set_error("nesting is too deep");
    skip_whitespace();
    if (position_ >= text_.size()) return set_error("unexpected end of input");
    const char c = text_[position_];
    if (c == '{') return parse_object(output, depth);
    if (c == '[') return parse_array(output, depth);
    if (c == '"') {
      output->kind = JsonValue::Kind::kString;
      return parse_string(&output->text);
    }
    if (c == 't') {
      if (!parse_literal("true")) return false;
      output->kind = JsonValue::Kind::kBool;
      output->boolean = true;
      return true;
    }
    if (c == 'f') {
      if (!parse_literal("false")) return false;
      output->kind = JsonValue::Kind::kBool;
      output->boolean = false;
      return true;
    }
    if (c == 'n') {
      if (!parse_literal("null")) return false;
      output->kind = JsonValue::Kind::kNull;
      return true;
    }
    if (c == '-' || (c >= '0' && c <= '9')) {
      output->kind = JsonValue::Kind::kNumber;
      return parse_number(&output->number);
    }
    error_ = "unexpected character";
    return false;
  }

  bool parse_object(JsonValue* output, int depth) {
    output->kind = JsonValue::Kind::kObject;
    ++position_; /* '{' */
    skip_whitespace();
    if (position_ < text_.size() && text_[position_] == '}') {
      ++position_;
      return true;
    }
    for (;;) {
      skip_whitespace();
      if (position_ >= text_.size() || text_[position_] != '"') {
        return set_error("expected object key string");
      }
      std::string key;
      if (!parse_string(&key)) return false;
      if (!expect(':')) return false;
      JsonValue value;
      if (!parse_value(&value, depth + 1)) return false;
      output->members.emplace_back(std::move(key), std::move(value));
      skip_whitespace();
      if (position_ >= text_.size()) return set_error("unexpected end of object");
      if (text_[position_] == ',') {
        ++position_;
        continue;
      }
      if (text_[position_] == '}') {
        ++position_;
        return true;
      }
      return set_error("expected ',' or '}'");
    }
  }

  bool parse_array(JsonValue* output, int depth) {
    output->kind = JsonValue::Kind::kArray;
    ++position_; /* '[' */
    skip_whitespace();
    if (position_ < text_.size() && text_[position_] == ']') {
      ++position_;
      return true;
    }
    for (;;) {
      JsonValue value;
      if (!parse_value(&value, depth + 1)) return false;
      output->elements.push_back(std::move(value));
      skip_whitespace();
      if (position_ >= text_.size()) return set_error("unexpected end of array");
      if (text_[position_] == ',') {
        ++position_;
        continue;
      }
      if (text_[position_] == ']') {
        ++position_;
        return true;
      }
      return set_error("expected ',' or ']'");
    }
  }

  bool parse_literal(const char* literal) {
    const size_t length = std::strlen(literal);
    if (text_.size() - position_ < length) return set_error("invalid literal");
    if (text_.compare(position_, length, literal) != 0) return set_error("invalid literal");
    position_ += length;
    return true;
  }

  bool parse_number(double* output) {
    const size_t start = position_;
    if (position_ < text_.size() && text_[position_] == '-') ++position_;
    while (position_ < text_.size()) {
      const char c = text_[position_];
      const bool is_digit = c >= '0' && c <= '9';
      if (!is_digit && c != '.' && c != 'e' && c != 'E' && c != '+' && c != '-') break;
      ++position_;
    }
    if (position_ == start) return set_error("expected number");
    const std::string token = text_.substr(start, position_ - start);
    char* end = nullptr;
    const double value = std::strtod(token.c_str(), &end);
    if (end == nullptr || *end != '\0') return set_error("invalid number");
    *output = value;
    return true;
  }

  bool parse_hex4(unsigned long* output) {
    if (text_.size() - position_ < 4) return set_error("truncated \\u escape");
    unsigned long value = 0;
    for (int i = 0; i < 4; ++i) {
      const int digit = hex_value(text_[position_ + static_cast<size_t>(i)]);
      if (digit < 0) return set_error("invalid \\u escape");
      value = (value << 4) | static_cast<unsigned long>(digit);
    }
    position_ += 4;
    *output = value;
    return true;
  }

  bool parse_string(std::string* output) {
    if (position_ >= text_.size() || text_[position_] != '"') {
      return set_error("expected string");
    }
    ++position_;
    output->clear();
    while (position_ < text_.size()) {
      const unsigned char c = static_cast<unsigned char>(text_[position_]);
      if (c == '"') {
        ++position_;
        return true;
      }
      if (c < 0x20) return set_error("control character in string");
      if (c != '\\') {
        output->push_back(static_cast<char>(c));
        ++position_;
        continue;
      }
      ++position_;
      if (position_ >= text_.size()) return set_error("truncated escape");
      const char escape = text_[position_];
      ++position_;
      switch (escape) {
        case '"':
          output->push_back('"');
          break;
        case '\\':
          output->push_back('\\');
          break;
        case '/':
          output->push_back('/');
          break;
        case 'b':
          output->push_back('\b');
          break;
        case 'f':
          output->push_back('\f');
          break;
        case 'n':
          output->push_back('\n');
          break;
        case 'r':
          output->push_back('\r');
          break;
        case 't':
          output->push_back('\t');
          break;
        case 'u': {
          unsigned long codepoint = 0;
          if (!parse_hex4(&codepoint)) return false;
          if (codepoint >= 0xD800 && codepoint <= 0xDBFF) {
            /* Combine a surrogate pair; a lone high surrogate is replaced. */
            if (text_.size() - position_ >= 6 && text_[position_] == '\\' &&
                text_[position_ + 1] == 'u') {
              position_ += 2;
              unsigned long low = 0;
              if (!parse_hex4(&low)) return false;
              if (low >= 0xDC00 && low <= 0xDFFF) {
                codepoint = 0x10000 + ((codepoint - 0xD800) << 10) + (low - 0xDC00);
              } else {
                codepoint = 0xFFFD;
              }
            } else {
              codepoint = 0xFFFD;
            }
          } else if (codepoint >= 0xDC00 && codepoint <= 0xDFFF) {
            codepoint = 0xFFFD;
          }
          if (codepoint > 0x10FFFF) codepoint = 0xFFFD;
          append_utf8(output, codepoint);
          break;
        }
        default:
          return set_error("invalid escape");
      }
    }
    return set_error("unterminated string");
  }

  const std::string& text_;
  size_t position_ = 0;
  std::string error_;
};

/* --- typed readers ------------------------------------------------------- */

bool read_int(const JsonValue& value, const char* what, int* output, std::string* error) {
  if (value.kind == JsonValue::Kind::kNumber) {
    const double number = value.number;
    if (!std::isfinite(number) || number < static_cast<double>(std::numeric_limits<int>::min()) ||
        number > static_cast<double>(std::numeric_limits<int>::max())) {
      *error = std::string("model.json: '") + what + "' is out of range";
      return false;
    }
    *output = static_cast<int>(number);
    return true;
  }
  if (value.kind == JsonValue::Kind::kBool) {
    *output = value.boolean ? 1 : 0;
    return true;
  }
  *error = std::string("model.json: '") + what + "' must be an integer";
  return false;
}

bool read_string(const JsonValue& value, const char* what, std::string* output,
                 std::string* error) {
  if (value.kind != JsonValue::Kind::kString) {
    *error = std::string("model.json: '") + what + "' must be a string";
    return false;
  }
  *output = value.text;
  return true;
}

bool read_float_array(const JsonValue& value, const char* what, std::vector<float>* output,
                      std::string* error) {
  if (value.kind != JsonValue::Kind::kArray) {
    *error = std::string("model.json: '") + what + "' must be an array";
    return false;
  }
  output->clear();
  for (const JsonValue& element : value.elements) {
    if (element.kind != JsonValue::Kind::kNumber) {
      *error = std::string("model.json: '") + what + "' must contain numbers";
      return false;
    }
    output->push_back(static_cast<float>(element.number));
  }
  return true;
}

/* --- schema mapping ------------------------------------------------------ */

bool parse_inputs(const JsonValue& root, OrtSidecar* sidecar, std::string* error) {
  const JsonValue* inputs = root.find("inputs");
  if (inputs == nullptr) return true;
  if (inputs->kind != JsonValue::Kind::kArray) {
    *error = "model.json: 'inputs' must be an array";
    return false;
  }
  for (const JsonValue& element : inputs->elements) {
    if (element.kind != JsonValue::Kind::kObject) {
      *error = "model.json: 'inputs' entries must be objects";
      return false;
    }
    OrtSidecarInput input;
    if (const JsonValue* name = element.find("name")) {
      if (!read_string(*name, "inputs[].name", &input.name, error)) return false;
    }
    if (const JsonValue* source = element.find("source")) {
      if (!read_string(*source, "inputs[].source", &input.source, error)) return false;
    }
    sidecar->inputs.push_back(std::move(input));
  }
  return true;
}

bool parse_outputs(const JsonValue& root, OrtSidecar* sidecar, std::string* error) {
  const JsonValue* outputs = root.find("outputs");
  if (outputs == nullptr) return true;
  if (outputs->kind != JsonValue::Kind::kArray) {
    *error = "model.json: 'outputs' must be an array";
    return false;
  }
  for (const JsonValue& element : outputs->elements) {
    if (element.kind != JsonValue::Kind::kObject) {
      *error = "model.json: 'outputs' entries must be objects";
      return false;
    }
    OrtSidecarOutput output;
    if (const JsonValue* name = element.find("name")) {
      if (!read_string(*name, "outputs[].name", &output.name, error)) return false;
    }
    if (const JsonValue* scale = element.find("scale")) {
      if (!read_int(*scale, "outputs[].scale", &output.scale, error)) return false;
    }
    if (const JsonValue* crop = element.find("crop")) {
      if (!read_int(*crop, "outputs[].crop", &output.crop, error)) return false;
    }
    sidecar->outputs.push_back(std::move(output));
  }
  return true;
}

bool parse_preprocess(const JsonValue& root, OrtSidecar* sidecar, std::string* error) {
  const JsonValue* preprocess = root.find("preprocess");
  if (preprocess == nullptr) return true;
  if (preprocess->kind != JsonValue::Kind::kObject) {
    *error = "model.json: 'preprocess' must be an object";
    return false;
  }
  if (const JsonValue* norm = preprocess->find("norm")) {
    if (!read_string(*norm, "preprocess.norm", &sidecar->preprocess.norm, error)) return false;
  }
  if (const JsonValue* channels = preprocess->find("channels")) {
    if (!read_int(*channels, "preprocess.channels", &sidecar->preprocess.channels, error)) {
      return false;
    }
  }
  if (const JsonValue* mean = preprocess->find("mean")) {
    if (!read_float_array(*mean, "preprocess.mean", &sidecar->preprocess.mean, error)) return false;
  }
  if (const JsonValue* std_values = preprocess->find("std")) {
    if (!read_float_array(*std_values, "preprocess.std", &sidecar->preprocess.std_values, error)) {
      return false;
    }
  }
  return true;
}

bool parse_constraints(const JsonValue& root, OrtSidecar* sidecar, std::string* error) {
  const JsonValue* constraints = root.find("constraints");
  if (constraints == nullptr) return true;
  if (constraints->kind != JsonValue::Kind::kObject) {
    *error = "model.json: 'constraints' must be an object";
    return false;
  }
  if (const JsonValue* tile_align = constraints->find("tileAlign")) {
    if (!read_int(*tile_align, "constraints.tileAlign", &sidecar->constraints.tile_align, error)) {
      return false;
    }
  }
  if (const JsonValue* fixed_size = constraints->find("fixedSize")) {
    if (!read_int(*fixed_size, "constraints.fixedSize", &sidecar->constraints.fixed_size, error)) {
      return false;
    }
  }
  if (const JsonValue* batch = constraints->find("batch")) {
    if (!read_int(*batch, "constraints.batch", &sidecar->constraints.batch, error)) {
      return false;
    }
  }
  if (const JsonValue* dtype = constraints->find("dtype")) {
    if (!read_string(*dtype, "constraints.dtype", &sidecar->constraints.dtype, error)) {
      return false;
    }
  }
  return true;
}

bool read_file(const std::string& path, std::string* contents, std::string* error) {
  std::FILE* file = fopen_utf8(path, "rb");
  if (file == nullptr) {
    *error = "model.json: cannot open " + path;
    return false;
  }
  contents->clear();
  char buffer[4096];
  for (;;) {
    const size_t read = std::fread(buffer, 1, sizeof(buffer), file);
    if (read > 0) {
      if (contents->size() + read > kMaxSidecarBytes) {
        std::fclose(file);
        *error = "model.json: file is larger than the 1 MiB limit";
        return false;
      }
      contents->append(buffer, read);
    }
    if (read < sizeof(buffer)) {
      if (std::ferror(file) != 0) {
        std::fclose(file);
        *error = "model.json: read error";
        return false;
      }
      break;
    }
  }
  std::fclose(file);
  return true;
}

}  // namespace

std::string ort_sidecar_path_for_model(const std::string& model_path) {
  const std::string directory = directory_of(model_path);
  if (directory.empty()) return std::string("model.json");
  return join_path(directory, "model.json");
}

OrtSidecar load_ort_sidecar(const std::string& model_path) {
  OrtSidecar sidecar;
  sidecar.path = ort_sidecar_path_for_model(model_path);
  if (!path_exists(sidecar.path)) {
    /* Missing sidecar is normal: auto-detect with documented defaults. */
    return sidecar;
  }
  sidecar.present = true;

  std::string text;
  if (!read_file(sidecar.path, &text, &sidecar.error)) return sidecar;

  JsonValue root;
  JsonParser parser(text);
  if (!parser.parse(&root, &sidecar.error)) return sidecar;
  if (root.kind != JsonValue::Kind::kObject) {
    sidecar.error = "model.json: document root must be an object";
    return sidecar;
  }

  if (!parse_inputs(root, &sidecar, &sidecar.error)) return sidecar;
  if (!parse_outputs(root, &sidecar, &sidecar.error)) return sidecar;
  if (!parse_preprocess(root, &sidecar, &sidecar.error)) return sidecar;
  if (!parse_constraints(root, &sidecar, &sidecar.error)) return sidecar;

  if (sidecar.constraints.tile_align < 0) {
    sidecar.error = "model.json: 'constraints.tileAlign' must be >= 0";
    return sidecar;
  }
  if (sidecar.constraints.fixed_size < 0) {
    sidecar.error = "model.json: 'constraints.fixedSize' must be >= 0";
    return sidecar;
  }
  for (const OrtSidecarOutput& output : sidecar.outputs) {
    if (output.crop < 0) {
      sidecar.error = "model.json: 'outputs[].crop' must be >= 0";
      return sidecar;
    }
  }
  if (sidecar.preprocess.channels <= 0) {
    sidecar.error = "model.json: 'preprocess.channels' must be > 0";
    return sidecar;
  }
  if (!sidecar.preprocess.mean.empty() && sidecar.preprocess.mean.size() != 3) {
    sidecar.error = "model.json: 'preprocess.mean' must be empty or have 3 values";
    return sidecar;
  }
  if (!sidecar.preprocess.std_values.empty() && sidecar.preprocess.std_values.size() != 3) {
    sidecar.error = "model.json: 'preprocess.std' must be empty or have 3 values";
    return sidecar;
  }
  for (float divisor : sidecar.preprocess.std_values) {
    if (!(divisor > 0.0f)) {
      sidecar.error = "model.json: 'preprocess.std' values must be > 0";
      return sidecar;
    }
  }
  if (sidecar.constraints.dtype != "fp32") {
    sidecar.error = "model.json: unsupported constraints.dtype '" + sidecar.constraints.dtype +
                    "' (only 'fp32' is supported)";
    return sidecar;
  }
  if (sidecar.constraints.batch > 1) {
    sidecar.error = "model.json: constraints.batch must be 1 (batched inference is not used)";
    return sidecar;
  }
  if (sidecar.constraints.tile_align == 0) sidecar.constraints.tile_align = 1;
  return sidecar;
}

}  // namespace chronos
