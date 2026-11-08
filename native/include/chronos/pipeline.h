/*
 * Project Chronos — per-session processing pipeline (C++ side of chronos.h).
 *
 * A Pipeline is *not* thread-safe (docs/CONTRACTS.md section 0, rule 4): one
 * Pipeline per Go worker. It owns the chosen backend, the tile work queue and
 * the progress/cancel atomics. Images are processed one after another through
 * the same session so the backend and the worker pool are reused.
 */
#ifndef CHRONOS_PIPELINE_H
#define CHRONOS_PIPELINE_H

#include <memory>
#include <string>

#include "chronos/options.h"

namespace chronos {

class Pipeline {
 public:
  ~Pipeline();

  Pipeline(const Pipeline&) = delete;
  Pipeline& operator=(const Pipeline&) = delete;

  /*
   * Creates a session pipeline.
   *   model_path empty -> stub backend (scale must be 1..4)
   *   model_path set   -> OpenVINO backend; fails when not compiled in
   * Returns nullptr and sets *err on failure (never throws through the C ABI).
   */
  static std::unique_ptr<Pipeline> create(const std::string& model_path, int scale,
                                          const Options& options, std::string* err);

  /*
   * load -> plan -> pad/crop -> parallel tile inference -> feather blend ->
   * save. Returns CHRONOS_OK, CHRONOS_ERR_IO / _INFER / _PARAM / _CANCELLED /
   * _UNKNOWN with *err set.
   */
  int process(const std::string& in_path, const std::string& out_path, int* out_width,
              int* out_height, std::string* err);

  int backend_id() const noexcept;
  int scale() const noexcept;
  std::string describe() const;

  int progress_done() const noexcept;
  int progress_total() const noexcept;
  void reset_progress() noexcept;
  void cancel() noexcept;
  bool cancelled() const noexcept;

 private:
  Pipeline();
  struct Impl;
  std::unique_ptr<Impl> impl_;
};

}  // namespace chronos

#endif /* CHRONOS_PIPELINE_H */
