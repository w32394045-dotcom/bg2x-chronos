/*
 * Project Chronos — OpenVINO backend.
 *
 * Only compiled for real with -DCHRONOS_WITH_OPENVINO=ON (the target then links
 * openvino::runtime). Without the option this TU still exists but only provides
 * openvino_available() == false and a factory that fails with a clear message,
 * so the rest of the code never needs #ifdefs.
 *
 * Design:
 *   - ov::Core -> read_model(path) (IR .xml or .onnx) -> compile_model(device)
 *   - The model's static input size defines the tile size; the output size
 *     defines the scale factor (verified against the requested scale).
 *   - N ov::InferRequest objects are kept in flight (N = options.threads, capped
 *     at 8). While request A is executing on the device, the host thread
 *     preprocesses the next tile and postprocesses the previous result, so CPU
 *     work is overlapped with inference (no OpenCV, ov::Tensor only).
 */
#include <cstdlib>
#include <memory>
#include <string>
#include <vector>

#include "chronos/backend.h"
#include "chronos/chronos.h"
#include "chronos/options.h"
#include "common.h"

#ifdef CHRONOS_WITH_OPENVINO

#include <algorithm>
#include <chrono>
#include <exception>
#include <stdexcept>
#include <thread>

#include <openvino/openvino.hpp>

#endif /* CHRONOS_WITH_OPENVINO */

namespace chronos {

bool openvino_available() noexcept {
#ifdef CHRONOS_WITH_OPENVINO
  return true;
#else
  return false;
#endif
}

#ifdef CHRONOS_WITH_OPENVINO
namespace {

constexpr int kMaxInflightRequests = 8;

std::string choose_device(ov::Core& core, int device_index) {
  if (const char* env = std::getenv("CHRONOS_OPENVINO_DEVICE")) {
    if (*env != '\0') return std::string(env);
  }
  std::vector<std::string> devices;
  try {
    devices = core.get_available_devices();
  } catch (...) {
    devices.clear();
  }
  std::vector<std::string> gpus;
  for (const std::string& device : devices) {
    if (device.rfind("GPU", 0) == 0) gpus.push_back(device);
  }
  if (!gpus.empty()) {
    const size_t index = device_index > 0 ? static_cast<size_t>(device_index) : 0;
    return index < gpus.size() ? gpus[index] : gpus.front();
  }
  return "CPU";
}

class OpenVINOBackend final : public IBackend {
 public:
  OpenVINOBackend(const std::string& model_path, int requested_scale, const Options& options)
      : requested_scale_(requested_scale) {
    core_ = ov::Core();
    model_ = core_.read_model(model_path);
    device_ = choose_device(core_, options.device_index);
    compiled_ = core_.compile_model(model_, device_);

    const ov::Output<const ov::Node> input = compiled_.input();
    if (input.get_partial_shape().is_dynamic()) {
      throw std::runtime_error("model input shape is dynamic; export it with a static tile size");
    }
    const ov::Shape input_shape = input.get_shape();
    if (input_shape.size() != 4 || input_shape[0] != 1 || input_shape[1] != 3 ||
        input_shape[2] != input_shape[3]) {
      throw std::runtime_error("unsupported model input shape (expected [1,3,H,W] with square H,W)");
    }
    tile_size_ = static_cast<int>(input_shape[2]);
    input_type_ = input.get_element_type();
    if (input_type_ != ov::element::Type_t::f32 && input_type_ != ov::element::Type_t::f16 &&
        input_type_ != ov::element::Type_t::u8) {
      throw std::runtime_error("unsupported model input precision (expected f32, f16 or u8)");
    }

    const ov::Output<const ov::Node> output = compiled_.output();
    if (output.get_partial_shape().is_dynamic()) {
      throw std::runtime_error("model output shape is dynamic");
    }
    const ov::Shape output_shape = output.get_shape();
    if (output_shape.size() != 4 || output_shape[0] != 1 || output_shape[1] != 3) {
      throw std::runtime_error("unsupported model output shape (expected [1,3,H,W])");
    }
    if (output_shape[2] == 0 || output_shape[3] == 0 ||
        output_shape[2] % input_shape[2] != 0 || output_shape[3] % input_shape[3] != 0) {
      throw std::runtime_error("model output size is not an integer multiple of the input size");
    }
    const size_t scale_h = output_shape[2] / input_shape[2];
    const size_t scale_w = output_shape[3] / input_shape[3];
    if (scale_h != scale_w) {
      throw std::runtime_error("model output aspect ratio does not match the input");
    }
    scale_ = static_cast<int>(scale_h);
    if (requested_scale > 0 && requested_scale != scale_) {
      throw std::runtime_error("requested scale " + std::to_string(requested_scale) +
                               " does not match model scale " + std::to_string(scale_));
    }
    output_type_ = output.get_element_type();
    if (output_type_ != ov::element::Type_t::f32 && output_type_ != ov::element::Type_t::f16 &&
        output_type_ != ov::element::Type_t::u8) {
      throw std::runtime_error("unsupported model output precision (expected f32, f16 or u8)");
    }
    patch_size_ = tile_size_ * scale_;
  }

  int id() const noexcept override { return CHRONOS_BACKEND_OPENVINO; }

  int native_scale() const noexcept override { return scale_; }

  std::string describe() const override { return "openvino/" + device_; }

  bool supports_batch() const noexcept override { return true; }

  int tile_size_for(int requested_tile_size) const noexcept override {
    (void)requested_tile_size;
    return tile_size_;
  }

  bool infer_tile(const uint8_t*, int, uint8_t*, int, TileWorkspace&,
                  std::string* err) override {
    set_error(err, "OpenVINO backend only supports the batched inference path");
    return false;
  }

  int run_batch(const Image& src, const TilePlan& plan, PatchSink& sink, const Options& options,
                const std::atomic<bool>& cancel, std::string* err) override {
    try {
      const int total = plan.count();
      if (total <= 0) return CHRONOS_OK;
      if (plan.tile_size != tile_size_ || plan.scale != scale_) {
        set_error(err, "OpenVINO backend received a plan for a different tile size or scale");
        return CHRONOS_ERR_INFER;
      }

      int inflight_target = options.effective_threads();
      inflight_target = std::max(2, inflight_target);
      inflight_target = std::min(kMaxInflightRequests, inflight_target);
      inflight_target = std::min(inflight_target, total);

      std::vector<ov::InferRequest> requests;
      requests.reserve(static_cast<size_t>(inflight_target));
      for (int i = 0; i < inflight_target; ++i) {
        requests.push_back(compiled_.create_infer_request());
      }

      std::vector<uint8_t> tile_rgb(static_cast<size_t>(tile_size_) *
                                    static_cast<size_t>(tile_size_) * 3);
      std::vector<uint8_t> patch(static_cast<size_t>(patch_size_) *
                                static_cast<size_t>(patch_size_) * 3);

      int next_tile = 0;
      int head = 0;
      int inflight = 0;
      auto start_tile = [&](ov::InferRequest& request, int tile_index) {
        const Tile& tile = plan.tiles[static_cast<size_t>(tile_index)];
        extract_tile_rgb(src, tile.x, tile.y, tile_size_, tile_rgb.data());
        fill_input(request, tile_rgb.data());
        request.start_async();
      };

      while (inflight < inflight_target && next_tile < total) {
        head_tile_[static_cast<size_t>(inflight)] = next_tile;
        start_tile(requests[static_cast<size_t>(inflight)], next_tile);
        ++next_tile;
        ++inflight;
      }

      while (inflight > 0) {
        if (cancel.load(std::memory_order_relaxed)) {
          for (ov::InferRequest& request : requests) {
            try {
              request.cancel();
            } catch (...) {
            }
          }
          set_error(err, "cancelled");
          return CHRONOS_ERR_CANCELLED;
        }
        ov::InferRequest& request = requests[static_cast<size_t>(head)];
        /* Poll cancel while waiting so a cancelled job stops quickly. */
        while (!request.wait_for(std::chrono::milliseconds(50))) {
          if (cancel.load(std::memory_order_relaxed)) {
            for (ov::InferRequest& pending : requests) {
              try {
                pending.cancel();
              } catch (...) {
              }
            }
            set_error(err, "cancelled");
            return CHRONOS_ERR_CANCELLED;
          }
        }

        const int finished_tile = head_tile_[static_cast<size_t>(head)];
        postprocess(request, patch.data());
        sink.emit(finished_tile, patch.data(), patch_size_);
        --inflight;
        ++head;
        head %= inflight_target;
        if (next_tile < total) {
          head_tile_[static_cast<size_t>(head)] = next_tile;
          start_tile(requests[static_cast<size_t>(head)], next_tile);
          ++next_tile;
          ++inflight;
        }
      }
      return CHRONOS_OK;
    } catch (const ov::Exception& exception) {
      set_error(err, std::string("OpenVINO inference failed: ") + exception.what());
      return CHRONOS_ERR_INFER;
    } catch (const std::exception& exception) {
      set_error(err, std::string("OpenVINO inference failed: ") + exception.what());
      return CHRONOS_ERR_INFER;
    } catch (...) {
      set_error(err, "OpenVINO inference failed: unknown error");
      return CHRONOS_ERR_INFER;
    }
  }

 private:
  void fill_input(ov::InferRequest& request, const uint8_t* tile_rgb) {
    ov::Tensor tensor = request.get_input_tensor();
    const int size = tile_size_;
    const size_t plane = static_cast<size_t>(size) * static_cast<size_t>(size);
    if (input_type_ == ov::element::Type_t::u8) {
      uint8_t* data = static_cast<uint8_t*>(tensor.data());
      for (int c = 0; c < 3; ++c) {
        uint8_t* dst = data + plane * static_cast<size_t>(c);
        for (int i = 0; i < size * size; ++i) {
          dst[i] = tile_rgb[i * 3 + c];
        }
      }
    } else if (input_type_ == ov::element::Type_t::f32) {
      float* data = static_cast<float*>(tensor.data());
      for (int c = 0; c < 3; ++c) {
        float* dst = data + plane * static_cast<size_t>(c);
        for (int i = 0; i < size * size; ++i) {
          dst[i] = static_cast<float>(tile_rgb[i * 3 + c]) / 255.0f;
        }
      }
    } else {
      ov::float16* data = static_cast<ov::float16*>(tensor.data());
      for (int c = 0; c < 3; ++c) {
        ov::float16* dst = data + plane * static_cast<size_t>(c);
        for (int i = 0; i < size * size; ++i) {
          dst[i] = ov::float16(static_cast<float>(tile_rgb[i * 3 + c]) / 255.0f);
        }
      }
    }
  }

  void postprocess(ov::InferRequest& request, uint8_t* patch) {
    ov::Tensor tensor = request.get_output_tensor();
    const int size = patch_size_;
    const size_t plane = static_cast<size_t>(size) * static_cast<size_t>(size);
    if (output_type_ == ov::element::Type_t::u8) {
      const uint8_t* data = static_cast<const uint8_t*>(tensor.data());
      for (int c = 0; c < 3; ++c) {
        const uint8_t* src = data + plane * static_cast<size_t>(c);
        for (int i = 0; i < size * size; ++i) patch[i * 3 + c] = src[i];
      }
      return;
    }
    if (output_type_ == ov::element::Type_t::f16) {
      const ov::float16* data = static_cast<const ov::float16*>(tensor.data());
      copy_float_patch([&](int c, int i) {
        return static_cast<float>(data[plane * static_cast<size_t>(c) + static_cast<size_t>(i)]);
      }, patch);
      return;
    }
    const float* data = static_cast<const float*>(tensor.data());
    copy_float_patch([&](int c, int i) {
      return data[plane * static_cast<size_t>(c) + static_cast<size_t>(i)];
    }, patch);
  }

  template <typename Getter>
  void copy_float_patch(Getter value, uint8_t* patch) {
    /* Heuristic for the output value range: many SR models emit [0,1] floats,
     * others already emit [0,255]. Auto-detect per patch. */
    float max_value = 0.0f;
    const int size = patch_size_;
    for (int c = 0; c < 3; ++c) {
      for (int i = 0; i < size * size; ++i) {
        max_value = std::max(max_value, value(c, i));
      }
    }
    const float gain = max_value <= 2.0f ? 255.0f : 1.0f;
    for (int c = 0; c < 3; ++c) {
      for (int i = 0; i < size * size; ++i) {
        const float scaled = value(c, i) * gain;
        patch[(i * 3) + c] = to_u8(scaled);
      }
    }
  }

  int requested_scale_ = 0;
  ov::Core core_;
  std::shared_ptr<ov::Model> model_;
  ov::CompiledModel compiled_;
  std::string device_;
  int scale_ = 1;
  int tile_size_ = 0;
  int patch_size_ = 0;
  ov::element::Type input_type_;
  ov::element::Type output_type_;
  int head_tile_[kMaxInflightRequests] = {0};
};

}  // namespace
#endif /* CHRONOS_WITH_OPENVINO */

std::unique_ptr<IBackend> create_openvino_backend(const std::string& model_path,
                                                  int requested_scale,
                                                  const Options& options, std::string* err) {
#ifdef CHRONOS_WITH_OPENVINO
  if (model_path.empty()) {
    set_error(err, "OpenVINO backend requires a model path");
    return nullptr;
  }
  try {
    return std::unique_ptr<IBackend>(new OpenVINOBackend(model_path, requested_scale, options));
  } catch (const ov::Exception& exception) {
    set_error(err, std::string("cannot load OpenVINO model: ") + exception.what());
  } catch (const std::exception& exception) {
    set_error(err, std::string("cannot create OpenVINO backend: ") + exception.what());
  } catch (...) {
    set_error(err, "cannot create OpenVINO backend: unknown error");
  }
  return nullptr;
#else
  (void)model_path;
  (void)requested_scale;
  (void)options;
  set_error(err,
            "OpenVINO backend is not compiled into this build of chronos "
            "(rebuild the native library with -DCHRONOS_WITH_OPENVINO=ON)");
  return nullptr;
#endif
}

}  // namespace chronos
