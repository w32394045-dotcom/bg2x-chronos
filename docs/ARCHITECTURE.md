# Project Chronos — 架构说明

> 状态：与 `docs/CONTRACTS.md`（冻结）配套的设计说明。实现细节以仓库代码为准，
> 最后更新：2026-11-08。
>
> 仓库：`github.com/w32394045-dotcom/bg2x-chronos`

## 1. 三层结构

```
┌──────────────────────────────────────────────────────────────────────┐
│ 第 1 层：UI（frontend/，Vue 3 + Vite + Tailwind，运行在 WebView 内）  │
│  - 队列、模型选择、并发/切块设置、进度、日志、抑制状态                │
│  - 只通过 src/lib/api.ts 调用 window.go.main.App.*，无 DOM 直连算力   │
└───────────────▲───────────────────────────────┬──────────────────────┘
                │ Wails Events (JSON)           │ Wails bindings (Promise)
                │ chronos:progress/job/log/...  │ App.GetAppInfo / UpscaleBatch ...
┌───────────────┴───────────────────────────────▼──────────────────────┐
│ 第 2 层：调度层（Go，仓库根 app.go + internal/）                      │
│  - internal/scheduler：队列、worker 池、并发上限、取消、事件节流       │
│  - internal/engine：Engine/Provider 抽象；cgo 实现或纯 Go 回退        │
│  - internal/models：嵌入 registry、模型缓存、下载/校验                │
│  - internal/config：用户设置 JSON                                     │
│  - internal/inhibit：睡眠/锁屏抑制（平台驱动）                        │
└───────────────▲───────────────────────────────┬──────────────────────┘
                │ CGO（仅文件路径）              │ chronos.h（冻结 C ABI）
                │ 返回码/宽高/进度                │
┌───────────────┴───────────────────────────────▼──────────────────────┐
│ 第 3 层：算力层（C++17，native/）                                     │
│  - Pipeline：load → plan → pad/crop → 并行 tile → 羽化拼接 → save     │
│  - Backend：stub（Lanczos/bilinear）或 OpenVINO（可选编译）           │
│  - 图像 IO：vendored stb_image / stb_image_write（不依赖 OpenCV）     │
│  - 每 session 一个 ThreadPool（fork/join，逐图复用）                  │
└──────────────────────────────────────────────────────────────────────┘
```

关键约束（全部来自 `docs/CONTRACTS.md`，实现必须遵守）：

1. **跨语言只传文件路径**，绝不传像素缓冲；图像读写发生在 C++ 内。
2. C++ 异常不得越过 `extern "C"` 边界，每个入口 `try/catch(...)`。
3. C++ 不依赖 OpenCV（ADR-001）；标量图像用 stb，张量用 OpenVINO 原生类型。
4. `chronos_session` 非线程安全：**每个 Go worker 独占一个 session**。
5. 并发上限硬编码 8（配置层 + UI 双重约束）。

## 2. 一次批处理的数据流

```
UI 拖入 N 张图
  │ UpscaleBatch(BatchRequest{files, modelId/modelPath, scale, tile, overlap,
  │                          concurrency, ...})
  ▼
app.go 校验参数 → scheduler 建 job（JobID）→ 返回 BatchResult{jobId, queued, skipped}
  │
  ▼ 每个并发 worker：
provider.Acquire() ──► Engine（= 一个 chronos_session + 一个 C++ ThreadPool）
  │
  ▼ engine.Process(ctx, {Input, Output})
native_cgo.go  C.CString(paths) → chronos_process(session, in, out, &w, &h, err...)
  │
  ▼ C++ Pipeline::process
stb 解码 → 规划 tile 网格 → replicate padding → 并行 tile 推理
        → 羽化权重累加 → resolve 成 8-bit → stb 写盘
  │
  │ 同时：Go 每 50ms chronos_session_progress() 采样，节流后发 chronos:progress
  │ 取消：ctx.Done() → chronos_session_cancel()，C++ 在 tile 边界返回 -6
  ▼
Result{Output, Width, Height, Backend, ElapsedMs} → scheduler 发事件 → UI 更新队列
```

- 进度以 **tile 数**计（`done/total` 原子自增），不是字节数，也不是百分比。
- 前端事件有节流（`chronos:progress` 每 tile 批 / 每 500ms 一次，见契约 §3.3）；
  Go 侧轮询间隔 50ms（`internal/engine/types.go: progressPollGap`）。
- 一个 job 的终态是 `JobSummary`（`chronos:job`），文件级状态在 `ProgressEvent.kind`。
- `Inhibit.AutoRelease` 为真时，job 结束后由 app 层自动 `SetInhibit({})` 并推一次
  `chronos:inhibit`——抑制的持有者是 Go app，不是 C++。

## 3. 为什么是 WebView + Go + C++

这个项目最初的原型路线是 bg2x 式的纯浏览器方案（WebAssembly + WebGPU）。换成桌面
进程内架构不是"更时髦"，而是被下面这些硬限制推着走：

| 约束 | 浏览器 / WASM / WebGPU | 本项目（WebView + Go + C++） |
| --- | --- | --- |
| 地址空间 | wasm32 线性内存 4 GiB 上限，实际可用更少；大图 + 模型权重同一份内存 | 原生 64 位地址空间，模型和大图分开分配 |
| 显存/推理后端 | 只有 WebGPU，算子覆盖和驱动差异大；无 OpenVINO/oneDNN | OpenVINO CPU/GPU AUTO，IR + FP16，失败可降级 stub |
| 线程 | Web Worker 受浏览器调度，无法绑定/锁定 OS 线程、无法控制 CPU 亲和 | 原生线程池，tile 级并行，可复用线程 |
| 文件批量 | 需要用户逐个授权、无法静默写回目录、大文件在内存中复制 | 原生文件系统，目录级批处理，路径直接传给 C++ |
| 系统集成 | 不能抑制睡眠/锁屏、不能挂托盘、不能注册文件关联 | 可调用 `SetThreadExecutionState` / logind |
| 离线与分发 | 首次加载模型/运行时依赖网络与缓存策略 | 安装包自带运行时；模型按需下载 |
| 调试/性能 | JS↔WASM 边界、GC 停顿、无法 perf 原生代码 | Go pprof、C++ profiler、ASAN/UBSan |

代价是真实的，不隐藏：安装包更大、需要按平台构建（Windows/Linux，WebView2/WebKitGTK
依赖）、CI 矩阵更复杂、macOS 尚未支持。这些都在 `docs/CI.md`、`docs/ROADMAP.md` 中
明确记录。

## 4. 线程模型

### Go 侧

- Wails 主 goroutine 跑窗口事件循环；所有 App 方法在 Wails 的 RPC 调度下执行，
  长任务必须异步（提交 job 后立即返回 `BatchResult`）。
- `internal/scheduler` 持有固定大小的 worker 池，大小 = 用户并发（1..8）。
- `internal/engine.Provider` 是**引擎池**：`Acquire` 取一个空闲 Engine，`Release`
  归还；池内引擎数不超过并发数。一个 Engine 内部是一个 `chronos_session`。
- 取消：`CancelJob` 设置 job 的 context cancel；正在处理该图的 worker 触发
  `chronos_session_cancel`，C++ 在下一个 tile 边界返回 `CHRONOS_ERR_CANCELLED`。

### C++ 侧

- 一个 session 一个 `ThreadPool`；线程在 session 创建时按 `options.threads`（0 =
  `hardware_concurrency`，上限 8）创建，逐图复用，避免每张图反复建线程。
- `ThreadPool::run` 是 fork/join 屏障：**调用线程也参与计算**（worker 0），因此
  `workers == 1` 时完全串行、不创建额外线程。
- tile 任务由原子游标分发；进度原子自增；取消位在 tile 边界检查——所以取消延迟
  上界约等于一个 tile 的推理时间。
- 异常由 worker 捕获，`run` 在调用线程重新抛出，最终被 C ABI 入口的 `try/catch`
  转成错误码，不会穿过 `extern "C"`。

### 前端

- 不做任何像素计算，只渲染事件；mock 模式（非 Wails 环境）用假数据驱动 UI。
- 并发滑块在 UI 层限制 1..8，并对 >4 的配置给出内存/散热警告（见 ARCHITECTURE §7）。

## 5. "只传路径"与零拷贝的诚实说明

"零拷贝"在本文档中只指**跨语言边界不拷贝像素**这一件事：

- Go→C++ 的参数是 `const char*` 文件路径；C++→Go 的返回值是错误码、宽高、
  进度计数、backend 名称等小数据。
- 图像从磁盘解码进 C++ 的 `std::vector<uint8_t>`，tile 推理、羽化累加、编码写盘
  全部在 C++ 进程内完成，Go 侧从头到尾看不到像素。
- 这样做的原因是所有权和生命周期：像素 buffer 若跨 CGO 传递，需要固定 Go 内存、
  约定释放方、处理 GC 移动与异常路径，任何一处错都会漏内存或 use-after-free。
- **但这不是全流程零拷贝**：C++ 内部至少有 tile 提取、patch 输出、浮点累加缓冲
  三份数据（见 §7）。"零拷贝"不能用来宣传内存占用低。

## 6. ADR-001：用 stb_image 而不是 OpenCV

**背景**：算力层需要读常见位图、写 PNG/JPEG/BMP。C++ 图像库的默认答案是 OpenCV。

**决策**：vendored `stb_image.h` / `stb_image_write.h`（`native/third_party/`），
不链接 OpenCV，也不引入 `cv::Mat`。图像容器是自定义的 8-bit interleaved `Image`
（`native/include/chronos/image.h`）。

**理由**：

- 依赖体积：OpenCV 运行时数百 MB，安装包和 CI 时间都不可接受；stb 是两个头文件。
- 许可证与合规：stb 为 public domain / MIT 双许可，分发无额外义务；OpenCV 为
  Apache-2.0，但会连带 FFmpeg 等可选依赖，审计面变大。
- 接口面：本项目只需要"整图 8-bit RGB/RGBA 读写"，不需要 `cv::Mat` 的通道布局、
  色彩空间、视频 IO、GUI 模块；引入 OpenCV 等于为 5% 的功能背 95% 的依赖。
- 构建：无 pkg-config、无动态库、无版本冲突；Windows/Linux 交叉构建简单。

**代价（必须说清楚）**：

- 只支持 stb 能解码的格式：PNG、JPEG、BMP、TGA、GIF、PSD、HDR、PIC 等；
  **不支持 WebP、AVIF、JPEG XL**。用户遇到 WebP 需要先转换。
- 不读取/应用 EXIF orientation：手机竖拍照片可能方向不对，需要用户预处理。
- 无 ICC 色彩管理：按 sRGB 处理，广色域图片会偏色。
- 输出端只写 PNG/JPEG/BMP，由输出扩展名决定；**不会静默改变格式**
  （`save_image` 对未知扩展名返回 `CHRONOS_ERR_IO`），Go 层默认输出 PNG 保证无损
  （契约 §3.4）。
- 解码/编码性能不如 libspng/libjpeg-turbo，但相对推理耗时可忽略。

**何时重审**：需要 WebP/AVIF 输入、16-bit 管线、或 ICC/EXIF 合规时，优先考虑
libspng + libjpeg-turbo + libwebp 的显式组合，而不是整体引入 OpenCV。

## 7. ADR-002：OpenVINO 作为可选后端

**背景**：AI 推理需要一个运行时。候选：OpenVINO、ONNX Runtime、TensorRT、
自研。用户硬件以 Intel CPU 为主，部分有 Intel/Arc GPU。

**决策**：CMake 选项 `-DCHRONOS_WITH_OPENVINO=ON|OFF`，**默认 OFF**；stub 后端
（高质量的 Lanczos/bilinear 重采样）永远编译可用；运行时若没有 OpenVINO 会话或
加载模型失败，返回明确错误码，绝不让进程崩溃。

**理由**：

- 分发体积：OpenVINO 运行时（含 CPU/GPU 插件）数百 MB，而多数用户只想做无模型
  的快速 2x-4x 重采样；把可选依赖强加给所有人不合理。
- 硬件差异：GPU 插件依赖驱动；Intel 核显/Arc/CPU 的可用性不同，构建期不应假设。
- CI 成本：带 OpenVINO 的构建需要额外下载与缓存；主 CI 必须保持轻量（见 openvino.yml
  的说明，该工作流只做可选后端的文档化验证，不阻塞主 CI）。
- 模型格式：OpenVINO IR（.xml/.bin）与 ONNX 都能加载，IR + FP16 有现成工具链
  （`scripts/convert_onnx_to_ir.py`）。

**为什么不是 ONNX Runtime**：ORT 的 GPU EP（CUDA/DirectML）分发复杂、Windows 上
DirectML 版本与运行时绑死、Linux CPU EP 还需注意线程配置；OpenVINO 的 `AUTO`
设备选择对 Intel 平台开箱即用，且 IR 转换工具在用户侧更可控。若将来需要
NVIDIA/AMD 加速，再评估 ORT/TensorRT，不影响当前 C ABI。

**后果**：

- 发布包若不编译进 OpenVINO（或不带 `chronos_native` 构建标签），只能使用
  stub/纯 Go 回退，**无法加载 AI 模型**；`chronos info` 会显示 backend 为 `stub`
  或 `go-lanczos`。发布构建在 CI 中固定带 `chronos_native`（见 `docs/CI.md`）。
- 模型需要 ONNX 或 OpenVINO IR；ONNX 可直接加载，但转 IR 后冷启动更快、可 FP16。
- stub 后端支持 scale 1..4；OpenVINO 后端以模型原生 scale 为准，请求的 scale 仅校验。

## 8. 内存预算

C++ 侧每张图的主要内存（`native/include/chronos/blending.h`）：

| 缓冲 | 大小 | 说明 |
| --- | --- | --- |
| 输入图 | `w × h × C` 字节 | 8-bit interleaved，stb 解码 |
| 累加缓冲 `accum` | `W × H × C × 4` 字节 | float，W=w×scale, H=h×scale |
| 权重和 `weight_sum` | `W × H × 4` 字节 | float，每像素一个分母 |
| 输出图 | `W × H × C` 字节 | resolve 后的 8-bit |
| 每 worker 的 tile/patch 缓冲 | 约 `tile² × C` + `(tile×scale)² × C` 字节 | 逐 tile 复用，不随图片数增长 |

以 1920×1080 输入、2x 放大、RGB 为例（输出 3840×2160）：

- `accum` = 3840×2160×3×4 B ≈ 99.5 MB
- `weight_sum` = 3840×2160×4 B ≈ 33.2 MB
- 合计约 **133 MB / session**；8 并发约 **1.06 GB**（不含运行时、模型权重、stb 输入输出）。
- 这类图片若用 4x，输出 7680×4320，上述两项变成约 531 MB / session，
  8 并发会超过 4 GB——**UI 的高并发警告不是摆设**。

模型权重（FP32 ONNX）另计：registry 中 2.5 MB 的动漫模型到 67 MB 的照片模型，
OpenVINO 加载后还有运行时开销。CI 从不在测试里下载真实大模型，只用本地生成的
微型 PNG 夹具（见 `scripts/ubuntu-smoke.sh`）。

## 9. 分层职责与不变量

| 层 | 可以做 | 不可以做 |
| --- | --- | --- |
| 前端 | 展示、聚合事件、调用 App 方法 | 直接计算像素、直连 C++、保存业务状态 |
| Go | 调度、参数校验、模型管理、抑制、错误映射 | 处理像素、持有 `chronos_session` 跨 worker 共享 |
| C++ | 解码/推理/拼接/编码、进度与取消 | 抛异常越过 C ABI、依赖 OpenCV、跨 session 共享状态 |

不变量：

- ABI 版本 1（`chronos_abi_version()`）；Go 启动自检，不符则降级到 go-lanczos 并写日志。
- 错误码三端共用：`0 OK | -1 IO | -2 MODEL | -3 INFER | -4 PARAM | -5 BACKEND |
  -6 CANCELLED | -99 UNKNOWN`。
- 前端 JSON 字段只增不删；模型 registry 单源（`internal/models/registry.json`，embed）。
- 退出路径必须释放：Engine→session、Provider→idle engines、Inhibit Manager→driver。

## 10. 相关文档

- 跨语言契约（冻结）：[CONTRACTS.md](CONTRACTS.md)
- 模型与转换：[MODELS.md](MODELS.md)
- 睡眠/锁屏抑制：[INHIBIT.md](INHIBIT.md)
- CI 与本地复现：[CI.md](CI.md)
- 路线图与真实限制：[ROADMAP.md](ROADMAP.md)
