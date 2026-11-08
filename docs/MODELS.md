# Project Chronos — 模型图谱与转换

> 最后更新：2026-11-08。模型清单的唯一事实来源是
> [`internal/models/registry.json`](../internal/models/registry.json)（由 Go `embed`
> 打包进二进制，契约 §6 中的 `models/registry.json` 指的就是这份逻辑单源；
> 仓库根目录的 `models/` 只是本地下载缓存目录）。

## 1. 内置模型（registry v1）

| id | 名称 | scale | kind | 体积 (MB) | 用途 | 上游 / 许可证 |
| --- | --- | ---: | --- | ---: | --- | --- |
| `realesr-general-x4v3` | Real-ESRGAN General x4v3 | 4 | general | 4.9 | 轻量通用模型，批量预处理/低配设备 | Real-ESRGAN · BSD-3-Clause |
| `real-esrgan-x4plus` | Real-ESRGAN x4plus | 4 | photo | 67 | 真实照片/老照片修复，细节最丰富 | Real-ESRGAN · BSD-3-Clause |
| `swin2sr-lightweight-x2` | Swin2SR Lightweight x2 | 2 | compressed | 8.1 | 压缩伪影抑制，速度较快 | Swin2SR · Apache-2.0 |
| `swin2sr-classical-x4` | Swin2SR Classical x4 | 4 | compressed | 55 | 保文字/线条，漫画与扫描件 | Swin2SR · Apache-2.0 |
| `swin2sr-realworld-x4` | Swin2SR RealWorld x4 | 4 | photo | 53 | 真实退化 4 倍，去噪与细节平衡 | Swin2SR · Apache-2.0 |
| `swin2sr-compressed-x4` | Swin2SR Compressed x4 | 4 | compressed | 55 | JPEG 压缩失真修复，漫画首选 | Swin2SR · Apache-2.0 |
| `real-cugan-2x` | Real-CUGAN 2x (HFA2k) | 2 | anime | 5.2 | 动漫 2 倍，线条锐利 | Real-CUGAN · MIT |
| `real-esrgan-animevideov3` | Real-ESRGAN AnimeVideo v3 | 4 | anime | 2.5 | 超轻量动漫模型，速度极快 | Real-ESRGAN · BSD-3-Clause |
| `real-esrgan-x4plus-anime` | Real-ESRGAN x4plus Anime | 4 | anime | 18 | 插画/动漫截图 4 倍，细节与平滑兼顾 | Real-ESRGAN · BSD-3-Clause |
| `real-esrgan-x4plus-anime-4b32f` | Real-ESRGAN x4plus Anime 4B32F | 4 | anime | 5.2 | 4B32F 轻量动漫模型，速度/画质折中 | Real-ESRGAN · BSD-3-Clause |

`kind` 取值：`photo`（真实照片）、`anime`（动漫/插画）、`compressed`（压缩失真修复）、
`general`（通用）。

## 2. 许可证说明

代码与权重的授权**必须分开看**：

| 上游项目 | 代码许可证 | 已核对来源 |
| --- | --- | --- |
| Real-ESRGAN（xinntao/Real-ESRGAN） | BSD-3-Clause | 上游仓库 LICENSE（GitHub license API 返回 `bsd-3-clause`） |
| Swin2SR（mv-lab/swin2sr） | Apache-2.0 | 上游 README 明确 "released under the Apache 2.0 license" |
| Real-CUGAN（bilibili/ailab/Real-CUGAN） | MIT | 上游 `Real-CUGAN/LICENSE`（MIT, Copyright 2022 bilibili） |

注意：

- **权重文件可能另有条款**。上表是上游代码仓库的许可证；Hugging Face 上的 ONNX
  导出件由第三方上传（registry 的 `url` 指向这些仓库），其 model card 可能声明不同
  的权重许可或使用限制。再分发/商用前请逐个核对 model card 与上游许可证。
- 本项目**不打包、不再分发任何模型权重**：registry 只有元数据与下载 URL，权重由
  用户执行 `chronos models pull <id>`（或 `scripts/fetch-models.sh <id>`）自行下载。
- 仓库内 `models/*.onnx` / `*.xml` / `*.bin` 已被 `.gitignore` 忽略，禁止提交权重。
- 上述上游仓库本身也可能依赖其他项目（如 BasicSR、Swin Transformer），其许可证
  需一并考虑；详见各项目 README 的 License 段落。

## 3. 下载与缓存

### 应用内

```
chronos models list          # 列出 registry + 本地是否已安装
chronos models pull <id>     # 下载到缓存目录（带 URL fallback，registry 有 sha256 时校验）
```

### 命令行工具（开发/离线）

```bash
bash scripts/fetch-models.sh --list
bash scripts/fetch-models.sh real-esrgan-x4plus
bash scripts/fetch-models.sh --dir /data/models swin2sr-lightweight-x2
bash scripts/fetch-models.sh --force real-cugan-2x
```

脚本只用 python3 标准库（`json` + `urllib`），按 `url` → `fallbackUrls` 顺序尝试，
`sha256` 非空时强校验；`--list` 打印 id/scale/kind/体积。

### 缓存位置（来自 `internal/models.DefaultDir()`：`os.UserCacheDir()/Chronos/models`）

| 平台 | 模型缓存 | 设置/数据目录（`internal/config.DataDir()`） |
| --- | --- | --- |
| Windows | `%LOCALAPPDATA%\Chronos\models` | `%APPDATA%\Chronos`（`settings.json`） |
| Linux | `${XDG_CACHE_HOME:-~/.cache}/Chronos/models` | `${XDG_CONFIG_HOME:-~/.config}/Chronos` |
| macOS（未支持构建） | `~/Library/Caches/Chronos/models` | `~/Library/Application Support/Chronos` |

缓存文件名由 registry 推导：`<id>.onnx`；若 `url` 以 `.xml` 结尾则为 `<id>.xml`
（`internal/models.models.go: PathFor`）。默认输出目录是 `~/Pictures/Chronos`
（`config.DefaultOutputDir`），可在 UI 中修改。

### sha256

当前 registry 的 `sha256` 字段全部为空，表示"下载后不校验"（脚本会打印实际哈希并
说明跳过校验）。为某个模型补充哈希后，`chronos models pull` 与
`scripts/fetch-models.sh` 都会强制校验，不匹配即删除临时文件并报错。维护建议：首次
人工下载后执行 `sha256sum <file>` 回填，并在 PR 中说明来源与日期。

## 4. ONNX → OpenVINO IR（FP16）

应用可以直接加载 `.onnx`（需要 OpenVINO 后端），但转成 IR 后冷启动更快、可用 FP16
把体积减半：

```bash
pip install openvino          # 老版本可用 openvino-dev
python scripts/convert_onnx_to_ir.py \
  --input models/real-esrgan-x4plus.onnx \
  --output-dir models/ir \
  --scale 4
```

输出 `models/ir/real-esrgan-x4plus_x4.xml` 与同名 `.bin`（`--scale` 只影响输出命名，
模型原生 scale 由网络结构决定）。脚本会打印输入/输出大小、压缩比与耗时；OpenVINO
未安装时以明确的退出码 2 报错，不影响应用本身。

然后把 IR 路径交给 CLI（或 UI 的"自定义模型路径"）：

```bash
chronos upscale -i in.png -o out.png -s 4 -m models/ir/real-esrgan-x4plus_x4.xml
```

注意：

- FP16 需要推理设备支持（Intel CPU 一般支持，老 CPU 由 OpenVINO 自动处理；
  GPU 插件通常支持）。转换失败时去掉 FP16 支持可使用 `ov.save_model(..., compress_to_fp16=False)`，
  或直接用原始 ONNX。
- IR 与 OpenVINO 版本相关：新版本 IR 可以在旧 runtime 上加载通常受限，建议同一台
  机器上转换；转换脚本兼容 `openvino.convert_model` / `openvino.save_model`
  （2023.3+）、`openvino.runtime.serialize`（2022.x）以及 `openvino.tools.ovc`
  等历史入口。
- 转换需要与模型匹配的算子支持；不支持的 ONNX 算子会在转换期报错，此时保留 ONNX
  直接加载（若 runtime 支持）或更换模型。

## 5. 添加一个新模型

1. 把模型文件（ONNX 或 IR）放到本地，确认能推理、记录原生 scale。
2. 在 `internal/models/registry.json` 的 `models` 数组追加一项：

   ```json
   {
     "id": "my-model-x4",
     "name": "My Model x4",
     "scale": 4,
     "kind": "general",
     "sizeMB": 12.3,
     "description": "一句话说明适用场景（中文，≤ 40 字）。",
     "url": "https://…/primary.onnx",
     "fallbackUrls": ["https://…/mirror.onnx"],
     "sha256": ""
   }
   ```

   `id` 只用于缓存文件名与 UI，稳定后不要改；`sha256` 建议填写（见 §3）。
3. 重新构建应用（registry 是 `go:embed`，不重建不会生效）：
   `wails build -tags "webkit2_41 chronos_native"`（Linux）/
   `wails build -tags chronos_native -platform windows/amd64`（Windows）。
4. 验证：`chronos models list` 能看到；`chronos models pull my-model-x4`；用
   `chronos upscale -i in.png -o out.png -s 4 -m <下载路径>` 跑通；检查输出尺寸。
5. 更新本文档 §1 表格与许可证说明；如果模型来自新上游，补 §2 的许可证行。
6. CI 不下载真实模型：冒烟测试只用 `--no-model`（stub 后端）或本地生成的小夹具，
   避免几百 MB 的网络依赖。

## 6. 没有模型时

`BatchRequest.ModelID` 为空且未指定 `ModelPath` 时使用 **stub 后端**：tile 化的
Lanczos3（`--fast` 用双线性）重采样，支持 scale 1..4，不是 AI 超分，不会产生幻觉
细节，但速度快、零下载。`chronos info` 的 `backend` 字段会显示 `stub`（原生库可用）
或 `go-lanczos`（未编译 `chronos_native` 时的纯 Go 回退）。
