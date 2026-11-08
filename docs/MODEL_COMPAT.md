# 模型兼容矩阵（SR Contract v2）

> 最后更新：2026-11-08。**这张表只写跑过的结论，不写推测。**
> 契约定义见 `docs/CONTRACTS.md` §8.3，导入流程见 `docs/PLAN-v0.3.md` §3。

## 怎么读

| 状态 | 含义 |
| --- | --- |
| ✅ 已验证 | 本仓库 CI 或真机跑过完整链路（导入 → 体检 → 试跑 → 拼接不变性） |
| 🟡 可导入 | 体检通过（形状/dtype/输入输出符合契约），但画质/性能未在本仓库复核 |
| ⛔ 不支持 | 明确拒绝，原因写在备注 |
| ⬜ 未测 | 还没拿到权重，不许猜 |

## 内置 registry 模型

| 模型 | 架构 | 状态 | 依据 |
| --- | --- | --- | --- |
| real-esrgan-animevideov3 | srvgg | ✅ 已验证 | 2.4MB 权重 SHA256 与 registry 一致；`native_ort` 套件用自造玩具模型覆盖同一算子组合（Conv/LeakyReLU/DepthToSpace） |
| realesr-general-x4v3 | srvgg | 🟡 可导入 | registry SHA256 来自 HF `lfs.oid`；未在本仓库跑权重 |
| real-esrgan-x4plus | rrdb | 🟡 可导入 | 同上 |
| real-esrgan-x4plus-anime | rrdb | 🟡 可导入 | 同上 |
| real-esrgan-x4plus-anime-4b32f | rrdb | 🟡 可导入 | 同上 |
| swin2sr-lightweight-x2 | swin2sr | 🟡 可导入 | 需要 `tileAlign=8`；registry 已声明 |
| swin2sr-classical-x4 / realworld-x4 / compressed-x4 | swin2sr | 🟡 可导入 | 同上 |
| real-cugan-2x | cugan | 🟡 可导入 | 需要 `tileAlign=2`；输出若带 padding 需在 sidecar 里写 `crop` |

## 自造测试模型（CI 用，许可证干净）

| 文件 | 用途 | 结论 |
| --- | --- | --- |
| `native/tests/fixtures/toy_sr_x2.onnx` | 动态 H/W 的 2x 模型 | ✅ scale=0（动态，靠 sidecar/请求值），报告 2 条 warning |
| `native/tests/fixtures/toy_sr_x2_static32.onnx` | 固定 32×32 输入的 2x 模型 | ✅ 探测出 `scale=2`，无 warning |

`chronos_model_inspect()` 的回归用例在 `native/tests/test_ort.cpp`（`model_inspect_*`），
随 `ctest -R native_ort` 在 CI 上运行。

## 社区模型（待测清单）

拿到权重后按下面的流程填表，**不要预先写"支持"**：

```bash
# 1) 下载并校验（registry 里有就先 pull）
chronos models pull real-esrgan-animevideov3

# 2) 导入（含自动探测）
chronos models import /path/to/model.onnx --name "4x-UltraSharp"

# 3) 自检（加载/尺寸/色彩/接缝/耗时）
chronos models selftest <id>
```

> CLI 子命令以 `chronos models --help` 为准；GUI 里对应「导入模型…」+「自检」。

| 模型 | 架构/算子特征 | 状态 | 备注 |
| --- | --- | --- | --- |
| 4x-UltraSharp（RRDBNet） | Conv/LeakyReLU/PixelShuffle | ⬜ 未测 | — |
| 4x_NMKD-Siax_200k | RRDBNet | ⬜ 未测 | — |
| Remacri | RRDBNet | ⬜ 未测 | — |
| HAT / DAT / DRCT | window attention / DCN | ⬜ 未测 | DCN 需 `com.microsoft` 域算子，可能被拒 |
| CodeFormer / GFPGAN | 多输出、固定 512 输入 | ⚠️ 实验性 | 需要人脸检测+对齐才有意义，整图跑出来是垃圾 |

## 已知不支持（明确拒绝）

| 类型 | 原因 |
| --- | --- |
| int8 / QDQ 量化模型 | 生态缺失；SR 画质损失大于速度收益，建议改 fp32/fp16 |
| 多输入图像（noise map / mask） | P5 只做常量填充 |
| opset < 7 | 概率极低，不做 |
| `.pb` / `.tflite` / `.param` | 非 ONNX |
| 缺少 `.data` 分片 | 导入时会把同目录 `.onnx.data` 一起拷贝；其它分片形式不支持 |

## Linux GPU 运行时矩阵（P3）

DirectML 是 Windows 专有，**Linux 上永远不会提供 DML**。Linux 的可选运行时：

| 硬件 | 运行时 | 体积 | 状态 |
| --- | --- | --- | --- |
| Intel CPU / iGPU / NPU | `openvino`（onnxruntime-openvino wheel，自包含） | 80.5MB | 🧪 实验性（exp） |
| NVIDIA 独显 | `cuda`（ORT 官方 gpu 包，需系统 CUDA 12 库） | 195.9MB | 🧪 实验性（exp） |
| **AMD（ROCm/MIGraphX）** | 不提供 | — | ⛔ 原因：ORT 官方已把 ROCm EP 移出主干，MIGraphX 需要用户自装 ROCm 且无官方预编译包 |
| **任意 Vulkan GPU** | 不提供 | — | ⛔ 原因：ORT 官方二进制不带 Vulkan EP，需要自行编译，ROI 过低 |
| SYCL / oneAPI | 不提供 | — | ⛔ 原因：官方包不带，需自行编译 |
| WebGPU（原生） | 不提供 | — | ⛔ 原因：原生侧仍是实验阶段 |

所有下载的运行时都必须先通过**子进程自检**（`chronos probe --json`）才会被激活；
自检失败会自动关闭该运行时并回落到内置 CPU 运行时，状态标会显示原因。


## 维护规则

1. 每次 CI `compat` 作业或真机验证后更新本表，写清**模型名 + 架构 + 结论 + 依据**。
2. 新增"不支持"条目必须给出用户可读的原因（会原样显示在导入失败的诊断里）。
3. 本表不承诺"所有超分 ONNX 都能跑"，只承诺**符合 SR Contract 的模型有可验证结论**。
