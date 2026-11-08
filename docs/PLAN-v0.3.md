# Chronos v0.3 实现计划（SR Contract v2）

> 状态：执行中。最后更新：2026-11-08。
> 本文是 v0.3 的唯一计划口径；实现完成后并入 `docs/ROADMAP.md` 与 `docs/CONTRACTS.md`。

## 0. 一句话目标

发布版必须能"选一个模型 → 下载 → 校验 → 导入 → 真的跑出结果"，并且告诉用户**到底是 GPU 还是 CPU 在算**；外部 SR 类 ONNX 模型可以导入，带体检与试跑；UI 增加纯白 / Claude 米白两套主题与缩放；提供一键本地构建。

## 1. 范围（锁定）

| 做 | 不做 |
| --- | --- |
| ONNX Runtime 作为默认推理运行时（CPU EP 内建） | int8 / QDQ 量化模型（生态缺失，明确拒绝并提示用 fp32/fp16） |
| 模型一条龙（下载/校验/安装/自检/卸载） | opset < 7 |
| 运行时组件自举（ORT / GPU 组件三级加载） | 多输入模型（P5 只做常量填充） |
| 后端真探测 + GPU/CPU 状态标 + `仅 CPU` 开关 | 人脸整图修复（检测+对齐+贴回，另立功能） |
| 外部模型导入（体检 + 自动探测 + 试跑 + 诊断） | 视频/序列帧 |
| 多输出选名、fp16、1 通道（后者最后） | 自动更新、安装包签名、macOS |
| 模型存储布局 v2（`models/<id>/`，顺带吃下 `.data`） | 任意 ONNX 兼容性承诺 |
| 主题（深色 / 纯白 / Claude 米白）+ 缩放 80–150% | 网络代理 UI、多语言 |

**对外表述**：支持符合 SR Contract（§3）的 ONNX 模型：1 输入 1 输出、RGB、NCHW、倍数 1–8；导入时自动校验并试跑。**不写"支持所有超分 ONNX"**。

## 2. ADR-003：ONNX Runtime 动态加载 + 版本锁定 1.24.4

**背景**：发布版目前只能跑 stub（Lanczos 重采样）——模型分支走 OpenVINO，而发布构建未开启
`CHRONOS_WITH_OPENVINO`，用户装了模型也只会得到 `not compiled into this build` 错误
（`native/src/pipeline.cpp`）。同时"GPU 加速"与"外部 ONNX 模型"两个需求都指向同一个运行时。

**事实核查（2026-11）**：

- ORT 最新为 v1.31.1，官方 Release 资产只有 `linux/win/osx` 的 CPU 包与 `gpu_cuda12/13` 包，
  **已不再提供 DirectML 资产**；NuGet 上 `Microsoft.ML.OnnxRuntime.DirectML` 停在 **1.24.4**。
- 官方文档已将 DirectML EP 定位为维护模式（"sustained engineering"），推荐 Windows 走 WinML
  （见 <https://onnxruntime.ai/docs/execution-providers/DirectML-ExecutionProvider.html>、
  PR <https://github.com/microsoft/onnxruntime/pull/25901>）。DirectML 支持到 opset 20，覆盖本项目全部 SR 模型。

**决策**：

1. **动态加载**：C++ 用 `LoadLibrary`/`dlopen` 解析 `OrtGetApiBase`，**构建期零链接依赖**。
   好处：MinGW/MSVC 都能编；同一份二进制可在 CPU 运行时与 GPU 运行时之间切换；
   运行时组件可以后下载（§6）。
2. **版本锁定 ORT 1.24.4**：CPU 与 DirectML 均取 1.24.4，避免同进程混用两个 ORT ABI。
3. **Windows GPU = DirectML**（NuGet 包 `Microsoft.ML.OnnxRuntime.DirectML` 1.24.4，用户点"启用 GPU"后下载）。
4. **Linux GPU 首版不做**（CUDA 包 245–450MB 且需 CUDA 运行时；Vulkan EP 不成熟）；Linux 走 CPU EP。
5. OpenVINO 后端保留为可选编译路径（Intel 专用优化），不再是"能跑模型"的前置条件。

**后果**：`chronos_backend_available(OPENVINO)` 不再等价于"能不能跑模型"；`chronos info` 需显示 ORT 是否可用；
模型加载失败的错误信息必须同时说明"ORT 未找到"和"本构建无 OpenVINO"两条路径。

## 3. SR Contract v2

### 3.1 模型契约（加载前体检必须逐条判定）

| 项 | 要求 | 不满足时 |
| --- | --- | --- |
| 输入数量 | 恰好 1 个图像张量（其余为 initializer 常量） | 拒绝，说明"多输入模型暂不支持" |
| 输入形状 | 4D `NCHW`，`N=1`，`C=3`（C=1/4 由适配层转换） | 其它拒绝 |
| 数据类型 | `float32`（`float16` 允许，按 EP 能力转换） | uint8/其它拒绝 |
| 输出数量 | ≥1，通过 `outputs[0].name` 指定；未指定时取第一个 4D 输出 | 全部输出都非 4D 时拒绝 |
| 输出形状 | 4D `NCHW`，`N=1`，`C=3`，`H_out = s·H_in`（crop 后） | 试跑校验失败即拒绝 |
| 放大倍数 | 整数 1–8（1 = 去噪/去块/修复） | 非整数/超 8 拒绝 |
| 对齐 | `constraints.tileAlign`（SwinIR 系 8/64）；`fixedSize` 固定输入模型 | 试跑不通过则提示补 sidecar |
| 归一化 | `preprocess.norm` ∈ `0-1` / `0-255` / `-1-1` | 探测冲突时警告 |
| 算子域 | 标准 ONNX 域；`com.microsoft` 域需白名单（P5） | custom 域拒绝并点名算子 |
| 文件 | `.onnx`（可带 `.onnx.data`） | 外部权重分片其它形式拒绝 |
| 体积 | ≤ 2GB | 拒绝 |

**明确不支持**：多个图像输入、多输出里挑不出 4D、LLM/文本编码器、检测分割、int8、`.pb/.tflite/.param`。

### 3.2 sidecar（`models/<id>/model.json`）

```json
{
  "inputs":  [{ "name": "input", "source": "image" }],
  "outputs": [{ "name": "", "scale": 4, "crop": 0 }],
  "preprocess": { "norm": "0-1", "channels": 3, "mean": [], "std": [] },
  "constraints": { "tileAlign": 1, "fixedSize": 0, "batch": 1, "dtype": "fp32" },
  "meta": { "license": "BSD-3-Clause", "arch": "rrdb", "notes": "" }
}
```

- `outputs[0].scale <= 0` ⇒ 由输出/输入空间比例推导，推导不出再用请求的 scale。
- `constraints.fixedSize > 0` ⇒ tile 取该值；`tileAlign > 0` ⇒ tile 向上取整到该倍数。
- `crop > 0` ⇒ 模型输出每边多 `crop` 像素，先裁再拼接。
- 缺省值：norm `0-1`、channels 3、crop 0、tileAlign 1、batch 1、dtype `fp32`。

### 3.3 "能用"验收（每个模型都要过，可自动化）

1. 导入/体检通过，或给出**具体**拒绝原因（第几层、哪个算子）。
2. 试跑 64×64、257×193、1024×1024 三种尺寸均成功。
3. 输出尺寸精确等于 `H·s × W·s`（crop 后）。
4. **拼接不变性**：tile=256 与 tile=512 结果逐像素一致（或差异 < 1e-3）。
5. 输出非全黑/全白/偏色（统计量检查）。
6. 任何失败都有可复制的诊断文本。

## 4. 存储布局 v2

```
<modelDir>/
  <id>/
    model.onnx            # 必需
    model.onnx.data       # 可选（外部权重，ORT 原生支持）
    model.json            # sidecar
```

- 首次访问时把旧的扁平 `models/<id>.onnx|.xml` **原子迁移**到新布局（幂等）。
- registry v2 增加 `arch / license / tileAlign / norm / sha256 / extraFiles`；`sha256` 必须非空
  （由 HF API 的 `lfs.oid` 生成，不下载权重）。
- 卸载 = 删除该目录；占用统计 = 目录大小之和。

## 5. 后端探测与状态标

C ABI 新增（**ABI 版本仍为 1**，只增不改）：

```c
#define CHRONOS_BACKEND_ORT 3
int chronos_ort_available(void);                       /* 运行时能否加载 ORT */
int chronos_backend_probe(char* buf, int buf_len);     /* JSON 能力报告 */
```

`chronos_backend_probe` 返回形如：

```json
{ "cpus": 16, "ort": { "available": true, "version": "1.24.4", "providers": ["CPU","DML"] },
  "openvino": { "available": false }, "sidecarFound": true, "tileAlign": 8,
  "fixedSize": 0, "scale": 4, "warnings": [] }
```

UI 三态 badge：🟢 `GPU · DirectML`｜⚪ `CPU · N 线程`｜🟡 `已降级 · CPU（原因）`。
`加速方式` 设置项：`auto` / `cpu`；语义是"禁止选择 GPU EP"，切换**对新任务生效**，不打断当前批。
状态标要能显示 GPU 参与节点数（graph partition 后有多少节点真的在 GPU），不允许只显示"GPU 已启用"。

## 6. 运行时组件与自举

统一下载器 `internal/artifact` 服务两类构件：

| 构件 | 来源 | 校验 | 备注 |
| --- | --- | --- | --- |
| 模型 | registry（HF → ModelScope → 镜像前缀） | SHA256 | 断点续传、可取消 |
| ORT CPU 运行时 | 官方 Release `onnxruntime-win-x64-1.24.4.zip` / `linux-x64-1.24.4.tgz` | SHA256 | 发布包可内建 |
| ORT DirectML 运行时 | NuGet `microsoft.ml.onnxruntime.directml` 1.24.4 | SHA256 | Windows 可选，用户点"启用 GPU"才下 |

加载顺序（运行时自举）：**内置 → 本地缓存（`<dataDir>/runtime/ort/<ver>/`）→ 下载**；
环境变量 `CHRONOS_ORT_DIR` 优先级最高（便于测试与高级用户）。

自举必须"显式"：检测到什么、缺什么、准备下什么、多大、校验值，可中断、可离线跳过。

## 7. 分批 PR 与依赖

| 批次 | 内容 | 依赖 | 验收 |
| --- | --- | --- | --- |
| P0 | 契约 v3、sidecar、存储布局 v2、registry v2 哈希 | — | 迁移测试 + `models hash --check` 全绿 |
| P1 | ORT 动态加载后端 + 玩具模型 + 拼接不变性 ctest | P0 sidecar 字段 | `native_ort` 套件；`chronos info` 显示 `ort/cpu` |
| P2 | artifact 下载器 + 模型一条龙 RPC/UI + 运行时自举 | P1 | 装完自动试跑并标"可用"；断点续传测试 |
| P3 | DirectML 组件 + 探测/状态标 + `仅 CPU` | P1、P2 | 无 GPU 机器自动降级且原因准确 |
| P4 | 外部模型导入（体检/自动探测/试跑/诊断/兼容矩阵） | P1–P3 | 5 个真实社区模型矩阵记录 |
| P5 | 多输出 → 1 通道 → contrib 白名单 → 多输入常量填充 | P4 | 每项"能用"六条全过 |
| P6 | 主题（深色/纯白/Claude 米白）+ 缩放 | —（可并行） | vitest + 人工三主题截图 |
| P7 | `build.bat` / `build.sh`（三档产物 + 依赖自举 + 离线） | P1、P3 | CI 里跑脚本本身 |

## 8. CI 变更

- `native`：新增 `native_ort`（缺 ORT 库时跳过）；ASan/UBSan 照旧。
- `go`：模型布局迁移、sidecar、下载器（httptest）、probe。
- `frontend`：主题/缩放单测；`pnpm build`。
- `compat`（`workflow_dispatch`）：真实模型矩阵，结果回填 `docs/MODEL_COMPAT.md`。
- `build-script`：跑 `build.sh` / `build.bat` 的低配档，防止脚本腐化。
- `package`：Windows zip 增加 ORT CPU 运行时；Linux 三包不变。

## 9. 风险

1. **体积**：ORT CPU 运行时 Windows ≈ 80MB、Linux ≈ 11MB；若内建，安装包显著变大 → 决定内建还是首启下载，见 P2。
2. **session 共享**：8 worker × 每 worker 一个 ORT session 会重复占内存/显存 → P1 先保证正确，P3 再优化。
3. **DirectML 首次编译延迟**（数十秒）→ UI 提示"首次使用 GPU 需预热"。
4. **固定 64×64 输入的模型**在大图上 tile 数爆炸 → 导入时警告。
5. **P0 布局迁移**必须在用户批量装模型前发布，否则要背历史包袱。
6. **sidecar 双端解析**（Go 与 C++）存在漂移风险 → 以本文 §3.2 为唯一 schema，两端测试都引用同一 fixture。

## 变更记录

| 日期 | 变更 |
| --- | --- |
| 2026-11-08 | 初版：范围锁定、ADR-003、SR Contract v2、布局 v2、分批 PR、CI 与风险 |
