/*
 * Project Chronos — thread pool implementation.
 */
#include "thread_pool.h"

namespace chronos {

ThreadPool::ThreadPool(int workers) : workers_(workers < 1 ? 1 : workers) {
  threads_.reserve(static_cast<size_t>(workers_ - 1));
  for (int i = 1; i < workers_; ++i) {
    threads_.emplace_back([this, i]() { worker_loop(i); });
  }
}

ThreadPool::~ThreadPool() {
  {
    std::lock_guard<std::mutex> lock(mutex_);
    stopping_ = true;
  }
  start_cv_.notify_all();
  join_all();
}

void ThreadPool::join_all() {
  for (std::thread& thread : threads_) {
    if (thread.joinable()) thread.join();
  }
  threads_.clear();
}

void ThreadPool::run(const std::function<void(int)>& fn) {
  if (workers_ <= 1) {
    fn(0);
    return;
  }

  std::exception_ptr caller_error;
  {
    std::lock_guard<std::mutex> lock(mutex_);
    current_ = &fn;
    {
      std::lock_guard<std::mutex> error_lock(error_mutex_);
      error_ = nullptr;
    }
    remaining_.store(workers_ - 1);
    ++generation_;
  }
  start_cv_.notify_all();

  try {
    fn(0);
  } catch (...) {
    caller_error = std::current_exception();
  }

  {
    std::unique_lock<std::mutex> lock(mutex_);
    done_cv_.wait(lock, [this]() { return remaining_.load() == 0; });
    current_ = nullptr;
  }

  if (caller_error) std::rethrow_exception(caller_error);
  std::exception_ptr worker_error;
  {
    std::lock_guard<std::mutex> error_lock(error_mutex_);
    worker_error = error_;
    error_ = nullptr;
  }
  if (worker_error) std::rethrow_exception(worker_error);
}

void ThreadPool::worker_loop(int index) {
  unsigned long long seen_generation = 0;
  const std::function<void(int)>* fn = nullptr;
  for (;;) {
    {
      std::unique_lock<std::mutex> lock(mutex_);
      start_cv_.wait(lock, [this, &seen_generation]() {
        return stopping_ || generation_ != seen_generation;
      });
      if (stopping_) return;
      seen_generation = generation_;
      fn = current_;
    }

    try {
      if (fn != nullptr) (*fn)(index);
    } catch (...) {
      std::lock_guard<std::mutex> error_lock(error_mutex_);
      if (!error_) error_ = std::current_exception();
    }

    if (remaining_.fetch_sub(1) == 1) {
      std::lock_guard<std::mutex> lock(mutex_);
      done_cv_.notify_all();
    }
  }
}

}  // namespace chronos
