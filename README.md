# Project Chronos · bg2x-chronos

> **把图像清晰度与处理时间一起重写。** 原生桌面 AI 超分工作站：**Webview (UI) + Go (调度) + C++ (算力)**。
> 承接 [isixe/bg2x](https://github.com/isixe/bg2x) 的产品理念，彻底抛弃浏览器沙箱的性能天花板。

[![CI](https://github.com/w32394045-dotcom/bg2x-chronos/actions/workflows/ci.yml/badge.svg)](https://github.com/w32394045-dotcom/bg2x-chronos/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

---

## 为什么重写 bg2x

bg2x 用 WebAssembly / WebGPU 在浏览器里跑超分，优点是零安装、隐私好；代价是：

| 痛点 | 浏览器方案（bg2x） | Chronos 原生方案 |
| --- | --- | --- |
| 内存 | WASM 线性内存 + JS GC，大图/批量易 OOM | C++ 自有缓冲区，按 tile 流水，峰值内存可预测 |
| 线程 | Web Worker 调度、主线程易卡顿 | Go worker pool + C++ 线程池，UI 永不阻塞 |
| GPU | WebGPU 支持面窄、驱动兼容差 | Intel OpenVINO（核显/独显）异步推理 |
| 大图 | 整图一次送进显存 | 256×256 切片 + 16px 羽化重叠，无接缝 |
| 计费/隐私 | 纯前端，无后端 | 同样全本地，不联网（除模型下载） |

**新增能力：处理期间可选择不进入睡眠、不关闭屏幕、不空闲锁屏**（Windows / Linux，见 [`docs/INHIBIT.md`](docs/INHIBIT.md)）。批量跑几十张老照片时，机器不会在中途睡着把任务打断。

## 架构

```text
┌──────────────────────────────┐
│ 前端层 Vue 3 + Vite + Tailwind │  drag&drop / 队列 / 并发滑块 / 前后对比 / 抑制开关
└──────────────┬───────────────┘
               │ Wails bindings (JSON) + Events
┌──────────────▼───────────────┐
│ 调度层 Go 1.26 (Wails 壳)      │  动态并发池 / 任务队列 / 命名落盘 / 模型下载 / 睡眠抑制
└──────────────┬───────────────┘
               │ CGO → chronos.h（只传路径）
┌──────────────▼───────────────┐
│ 算力层 C++17 + OpenVINO        │  切片 → 异步推理 → 羽化拼接 → PNG 落盘
└──────────────────────────────┘
```

设计约束（来自架构设计书，全部有实现与测试）：

1. **跨语言只传文件路径**，像素数据永远不跨 CGO 边界。
2. **C++ 零异常穿越**：每个 `extern "C"` 入口 `try/catch(...)`，错误码返回，Go 侧映射为 sentinel error。
3. **动态并发**：UI 滑块 1–8 实时收缩/扩张任务池，正在跑的任务不会被粗暴掐断。
4. **切片 + 羽化**：默认 256px tile / 16px overlap，线性羽化权重归一化，消除网格缝。
5. **硬上限保护**：并发上限 8（`engine.MaxConcurrency`），UI 高并发时提示风险。
6. **没有原生库也能跑**：`chronos_native` 构建标签缺失时自动降级为纯 Go 的 Lanczos3 切片实现（适合无 OpenVINO 环境和 `go test`），可用 `--no-model` 体验。

## 功能

- 拖拽/多选批量超分，队列级进度（按 tile 实时推进）、取消、失败隔离
- 动态并发滑块（1–8）+ 高级选项（tile / overlap / threads / fast）
- 模型管理：内置 10 个 Real-ESRGAN / Swin2SR / Real-CUGAN 模型清单，按需下载、SHA256 校验、多镜像回退
- 前后对比滑块、最近结果、日志控制台、引擎状态（backend/device/ABI）
- **睡眠/熄屏/锁屏抑制**：`sleep`、`display`、`lock` 三开关 + 任务结束自动释放；Windows 用 `SetThreadExecutionState`，Linux 用 `systemd-logind Inhibit` + `org.freedesktop.ScreenSaver`
- 无头 CLI：可与脚本/服务端流水线集成，也是 CI 在 Ubuntu 上验收算力层的方式

## 快速开始

### 直接运行（无需 C++ 工具链，自动使用 Go 回退引擎）

```bash
go run ./cmd/chronos info
go run ./cmd/chronos gen -o test.png -w 512 -h 512
go run ./cmd/chronos upscale -i test.png -s 2 --no-model
```

### 构建原生引擎（推荐）

```bash
# Linux
cmake -S native -B native/build -G Ninja -DCMAKE_BUILD_TYPE=Release
cmake --build native/build
ctest --test-dir native/build --output-on-failure

# Windows (MinGW-w64 + Ninja)
cmake -S native -B native/build -G Ninja -DCMAKE_BUILD_TYPE=Release -DCMAKE_C_COMPILER=gcc -DCMAKE_CXX_COMPILER=g++
cmake --build native/build
ctest --test-dir native/build --output-on-failure
```

启用 AI 推理（需要 OpenVINO 运行时）：

```bash
cmake -S native -B native/build -G Ninja -DCHRONOS_WITH_OPENVINO=ON -DOpenVINO_DIR=<openvino>/runtime/cmake
cmake --build native/build
```

### 构建桌面应用

```bash
cd frontend && pnpm install && pnpm build && cd ..
go install github.com/wailsapp/wails/v2/cmd/wails@latest
wails build -tags chronos_native            # Windows: build/chronos.exe
wails build -tags "webkit2_41 chronos_native"   # Ubuntu 24.04
```

产物：Windows `build/bin/chronos.exe`；Linux 同目录二进制 + `libs/libchronos.so`（或用 `scripts/package-linux.sh` 打包 .deb/.tar.gz）。

### 模型

```bash
chronos models list
chronos models pull swin2sr-compressed-x4     # 漫画/压缩修复（4x）
chronos upscale -i manga.png --model-id swin2sr-compressed-x4
```

ONNX → OpenVINO IR（FP16）转换、模型许可与缓存位置见 [`docs/MODELS.md`](docs/MODELS.md)。

## CLI 速查

```text
chronos upscale -i a.png,b.jpg -o out/ -s 4 [--model m.onnx | --model-id id] [--tile 256] [--overlap 16] [--threads 0] [--fast] [--no-model] [--json]
chronos info [--json]            # 引擎/平台/目录诊断
chronos gen -o test.png -w 512 -h 512
chronos models list|pull|path|import|dir
chronos inhibit on --sleep --display --lock [--duration 1h] [--json]
chronos inhibit status --json
chronos bench --size 512 --iters 3 -s 2
```

## 目录结构

```text
main.go / app.go            Wails 桌面入口与绑定（JSON 契约见 docs/CONTRACTS.md）
cmd/chronos/                无头 CLI
internal/engine/            引擎接口、CGO 原生绑定（chronos_native）、纯 Go 回退、引擎池
internal/scheduler/         动态并发任务队列（1–8 实时调整、取消、命名、失败隔离）
internal/inhibit/           睡眠/熄屏/锁屏抑制（Windows/Linux 驱动 + 可测试状态机）
internal/models/            模型注册表（embed）、下载/校验/导入
internal/config/            设置持久化
native/                     C++17 核心：切片/羽化/Lanczos/OpenVINO 后端/C ABI/测试
frontend/                   Vue 3 + Vite + Tailwind 界面
.github/workflows/          CI：双平台原生构建+ctest、Go 测试、前端构建、Wails 打包、Release
docs/                       架构、契约、模型、抑制、CI、路线图
```

## 测试

| 层 | 命令 | 覆盖 |
| --- | --- | --- |
| C++ | `ctest --test-dir native/build --output-on-failure` | 切片覆盖/无缝隙、羽化归一化、常量图不变性、Lanczos、IO、pipeline、取消、C API 错误码 |
| Go | `go test ./...` | 动态并发实测、取消、命名冲突、失败隔离、抑制状态机、模型下载校验、配置 |
| Go+CGO | `go test -tags chronos_native ./internal/engine/...` | C ABI 边界、真实图像处理 |
| UI | `cd frontend && pnpm test` | 队列/进度合并等纯逻辑 |
| 端到端 | `scripts/ubuntu-smoke.sh` / `scripts/windows-smoke.ps1` | 无头 CLI 图像尺寸断言、抑制状态、引擎信息 |

CI 在 **ubuntu-latest + windows-latest** 上跑完整矩阵，Ubuntu 任务同时验证 headless 算力层与 `.deb` 打包。

## 与 bg2x 的关系

- 产品定位与模型选型继承 bg2x（MIT，Copyright (c) 2026 isixe），图标沿用其资源；
- 本仓库是**独立实现**：UI 重写，算力层从 Web GPU/WASM 迁到 C++/OpenVINO，新增无头 CLI 与系统电源/锁屏抑制；
- 如需纯浏览器、免安装版本，请使用原项目 [isixe/bg2x](https://github.com/isixe/bg2x)。

## License

MIT。模型权重版权归各自上游（Real-ESRGAN / Swin2SR / Real-CUGAN），详见 [`docs/MODELS.md`](docs/MODELS.md)。
