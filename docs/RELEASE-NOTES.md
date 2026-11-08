# Chronos 0.3.0 — 真正能跑模型的版本

> 这是 Chronos 第一次做到：**装一个模型 → 下载 → 校验 → 装好 → 自动试跑 → 真的出图**。
> 0.2.0 及更早的发布包只能做 Lanczos 重采样（stub 引擎），本版把推理运行时补上了。

## 亮点

- **内置 ONNX Runtime 运行时**（动态加载，构建期零链接依赖）：发布包开箱即可加载 `.onnx` 模型。
- **模型一条龙**：一键下载（断点续传/镜像回退/SHA256 校验）→ 安装 → 装完自检 → 可用。
- **真的 GPU/CPU 状态标**：显示后端、执行后端、设备与降级原因；提供 **自动 / 仅 CPU** 开关。
- **外部模型导入**：符合 SR Contract 的 ONNX（1 输入 1 输出、RGB、NCHW、x1–x8）可导入；导入时读取图元数据自动推导倍率，跑一次"加载/尺寸/颜色/拼接"自检，并生成可复制诊断。
- **主题与缩放**：深色 / 纯白 / Claude 米白三套主题；Ctrl+= / Ctrl+- / Ctrl+0 缩放（浏览器模式用 Ctrl+Shift 组合，设置里也提供了按钮）。
- **Linux GPU 运行时（实验性）**：CUDA（NVIDIA）与 OpenVINO（Intel CPU/iGPU/NPU）作为可选下载，全部先通过**子进程自检**才会启用；失败自动关闭并回落 CPU。DirectML 是 Windows 专有，Linux 永不提供。
- **一键构建脚本**：`scripts/build.bat`（Windows）与 `scripts/build.sh`（Linux）。

## 下载

| 平台 | 文件 | 说明 |
| --- | --- | --- |
| Windows | `chronos-0.3.0-windows-amd64.zip` | GUI（Wails）+ CLI + `web/` + `runtime/ort/`；无 WebView2 时自动进入浏览器模式 |
| Linux | `chronos_0.3.0_amd64.deb` | GUI，依赖 GTK3 / WebKit2GTK 4.1，含 ORT CPU 运行时 |
| Linux | `chronos-0.3.0-linux-amd64.tar.gz` | GUI 便携版 |
| Linux | `chronos-0.3.0-linux-amd64-web.tar.gz` | 零 GUI 依赖：CLI + `web/` + `libs/` + `chronos-web` 启动器 |
| 全部 | `SHA256SUMS` | 请先校验再运行 |

安装后：GUI 直接双击；无桌面环境用 `./chronos serve --open`（web 包）或 `chronos-cli serve`（Windows zip 内）。

## 升级须知

1. **模型目录换成新布局**：`<模型目录>/<id>/model.onnx` + `model.json`，旧的扁平 `models/<id>.onnx` 会在首次访问时自动迁移。
2. **模型校验变严格**：registry 现在带真实 SHA256（来自上游 LFS 元数据），下载不匹配会被拒绝。
3. 设置文件新增 `theme` / `uiScale` / `accelMode` / `gpuRuntimeDisabled` 字段，旧设置自动兼容。
4. 首次使用 GPU：在加速标弹层里点"下载 GPU 运行时"，安装完成后**重启应用**才会真正启用（运行时在进程启动时加载）。

## 已知限制（如实）

- Linux 的 CUDA / OpenVINO 运行时标记为 **exp**：已做下载/校验/解包/子进程门禁，但**尚未在真实 Linux + 对应 GPU 上完成推理验证**；门禁失败会自动关闭。
- CUDA 运行时只带 ONNX Runtime 的 CUDA EP，用户机器仍需 NVIDIA 驱动与 CUDA 12 用户态库。OpenVINO 运行时自包含（约 80MB），覆盖 Intel CPU/iGPU/NPU。
- 不支持：int8/QDQ 量化模型、多输入图像模型、opset < 7、AMD（ROCm/MIGraphX 无官方预编译包）、Vulkan/SYCL/WebGPU、macOS。
- 模型导入目前不会自动探测归一化范围（默认 `0-1`）；如遇全黑/全白输出，请在 `model.json` 的 `preprocess.norm` 里改成 `0-255` 或 `-1-1`。
- 无自动更新、无遥测；日志只在本机。

## 验证情况

- Windows：native ctest 11/11（含真实 ONNX Runtime 推理与拼接不变性）、Go 测试全绿、前端 100 个 vitest 全过。
- Linux（CI）：native ctest + ASan/UBSan、ORT 真实推理作业、Go/cgo 集成、浏览器模式冒烟、一键构建脚本、打包。
- 未覆盖：本仓库 CI 没有 GPU 硬件，GPU 推理只能靠门禁 + 用户真机反馈。
