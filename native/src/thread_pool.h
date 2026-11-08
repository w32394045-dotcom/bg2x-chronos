/*
 * Project Chronos — minimal reusable fork/join worker pool.
 *
 * The calling thread always participates as worker 0, so `workers` includes it
 * (workers == 1 means completely sequential, no background threads are spawned).
 * Threads are created once per session and reused for every image; a run()
 * call is a fork/join barrier. Exceptions thrown by any worker are captured and
 * rethrown by run() on the calling thread, keeping exception escape explicit.
 */
#ifndef CHRONOS_THREAD_POOL_H
#define CHRONOS_THREAD_POOL_H

#include <atomic>
#include <condition_variable>
#include <cstddef>
#include <exception>
#include <functional>
#include <mutex>
#include <thread>
#include <vector>

namespace chronos {

class ThreadPool {
 public:
  explicit ThreadPool(int workers);
  ~ThreadPool();

  ThreadPool(const ThreadPool&) = delete;
  ThreadPool& operator=(const ThreadPool&) = delete;

  int size() const noexcept { return workers_; }

  /* Runs fn(worker_index) on every worker, blocks until all of them return. */
  void run(const std::function<void(int)>& fn);

 private:
  void worker_loop(int index);
  void join_all();

  const int workers_ = 1;
  std::vector<std::thread> threads_;

  std::mutex mutex_;
  std::condition_variable start_cv_;
  std::condition_variable done_cv_;
  const std::function<void(int)>* current_ = nullptr;
  std::atomic<int> remaining_{0};
  unsigned long long generation_ = 0;
  bool stopping_ = false;

  std::mutex error_mutex_;
  std::exception_ptr error_;
};
}  // namespace chronos

#endif /* CHRONOS_THREAD_POOL_H */
