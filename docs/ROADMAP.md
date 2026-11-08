# Project Chronos — 路线图与状态

> 最后更新：2026-11-08。阶段划分沿用架构设计书 Phase 1–4；状态按**本仓库当前代码**
> 直接盘点（不按计划口径），未跑通的路径一律标"进行中"或"计划中"。

## 状态总览

| 阶段 | 主题 | 状态 |
| --- | --- | --- |
| Phase 1 | 核心骨架：契约、C++ 算力层、Go 调度、CI | 进行中（收尾） |
| Phase 2 | 模型推理与批量处理 | 进行中 |
| Phase 3 | 桌面体验、抑制、打包分发 | 进行中（部分就绪） |
| Phase 4 | 性能、生态与平台扩展 | 计划中 |

---

## Phase 1 — 核心骨架

**目标**：三层接口冻结、无模型（stub）路径端到端可跑、CI 可复现。

| 交付物 | 状态 | 说明 |
| --- | --- | --- |
| `docs/CONTRACTS.md` v1（冻结） | 完成 | C ABI / JSON / 事件契约，改动需同步三端 |
| C++ 算力层：tile 切片、replicate padding、羽化拼接、取消 | 进行中 | `native/src/{tiling,blending,pipeline,resample}.cpp` 已实现，ctest 用例随实现补齐 |
| stub 后端（Lanczos3 / `--fast` 双线性） | 进行中 | `backend_stub.cpp`，scale 1..4 |
| stb_image 图像 IO（无 OpenCV，ADR-001） | 完成 | `native/third_party/stb_*`、`native/src/image.cpp` |
| C++ 异常不越过 C ABI | 完成 | 所有入口 `try/catch(...)` + `chronos_last_error` 线程局部错误 |
| Go engine 抽象 + 纯 Go 回退 | 完成 | `internal/engine/{types,provider,fallback,native_cgo,native_stub}.go` |
| Go 调度（队列、并发上限 8、取消、事件） | 完成 | `internal/scheduler`，含单元测试 |
| Wails 壳（RPC + 事件） | 进行中 | `wails.json`、`build/` 资源就绪；根包 `main.go`/`app.go` 由 Lead 集成中 |
| CLI `cmd/chronos` | 进行中 | 子命令契约见 `docs/CI.md` §8；CI 冒烟依赖它 |
| 前端壳（Vue 3 + Vite + Tailwind，mock 模式） | 完成 | `frontend/src/**` 组件与 `api.ts` 薄封装 |
| CI（native/Go/集成/前端/打包）+ 本套文档 | 完成 | `.github/workflows/ci.yml`、`scripts/*`、`docs/*` |

**Phase 1 验收条件**（全部满足才可收尾）：

1. `ctest` 在 Release 与 ASan/UBSan 两套构建下通过；
2. `CGO_ENABLED=1 go test -tags chronos_native ./internal/engine/...` 在两平台通过；
3. `chronos upscale --no-model` 把 64×48 PNG 准确放大为 128×96（两平台冒烟脚本）；
4. `wails build` 在 Linux（webkit2_41）与 Windows 通过，`chronos info` 显示 `stub`；
5. 前端 `pnpm test` / `pnpm build` 通过，根包能 embed `frontend/dist`。

## Phase 2 — 模型推理与批量

| 交付物 | 状态 | 说明 |
| --- | --- | --- |
| OpenVINO 后端（可选编译，ADR-002） | 进行中 | `native/src/backend_openvino.cpp` + CMake `CHRONOS_WITH_OPENVINO`；主 CI 不开启，`openvino.yml` 手动验证 |
| ONNX / IR 模型加载 + 错误码 `-2` | 进行中 | `chronos_session_create` 模型路径分支；失败回写错误信息 |
| 模型 registry + 下载/校验 | 完成 | `internal/models`（embed `registry.json`、URL fallback、sha256、缓存目录） |
| `chronos models list|pull` | 进行中 | 依赖 CLI 根包集成 |
| ONNX→IR FP16 转换工具 | 完成 | `scripts/convert_onnx_to_ir.py`（兼容多代 OpenVINO API） |
| 批量队列、并发 1..8、取消、进度事件 | 完成 | scheduler + provider + 事件，契约 §3.3 |
| 切块/重叠/线程数高级参数 | 完成 | Config 归一化与校验（`internal/engine/types.go`） |
| 真实模型端到端验证（各 registry 模型） | 计划中 | 需要下载权重与本机 OpenVINO；CI 不做（体积/网络） |

**Phase 2 验收条件**：至少 3 个 registry 模型（覆盖 photo/anime/compressed）在
CPU 与（有条件的）GPU 上跑通 2x/4x；`chronos info` 报告 `openvino` 与设备；
取消在 tile 边界内生效（≤ 一个 tile 的延迟）；进度与 `JobSummary` 数据一致。

## Phase 3 — 桌面体验与分发

| 交付物 | 状态 | 说明 |
| --- | --- | --- |
| 睡眠/熄屏/空闲锁屏抑制（Windows + Linux） | 完成 | `internal/inhibit` 平台驱动 + 状态机 + 单测，见 `docs/INHIBIT.md` |
| 抑制 UI（开关、自动释放、平台注意事项） | 进行中 | `frontend/src/components/InhibitCard.vue`，事件 `chronos:inhibit` |
| 设置持久化（并发、输出目录、上次模型等） | 完成 | `internal/config`（`settings.json`，损坏安全回退） |
| 错误恢复（坏图跳过、失败清单、日志） | 进行中 | `BatchResult.skipped` / `ProgressEvent.kind=failed` 已定义，UI 呈现待联调 |
| Linux 打包（.deb + .tar.gz + SHA256SUMS） | 完成 | `scripts/package-linux.sh`，CI `package` job |
| Windows 打包（zip：exe + dll + registry） | 完成 | CI `package` job（PowerShell 组装） |
| Release 流水线（tag v* → GitHub Release） | 完成 | `.github/workflows/release.yml`，softprops/action-gh-release@v2 |
| 自动更新 | 计划中 | 需要签名/更新源设计，Phase 4 再评估 |
| 安装包签名（Windows Authenticode / Linux 仓库签名） | 计划中 | 依赖发布基础设施 |

**Phase 3 验收条件**：在干净的 Ubuntu 24.04 与 Windows 11 上，从 Release 资产安装
后能完成一次 10 张图批量任务；抑制在任务期间生效、结束后释放；`.deb` 的
`/usr/bin/chronos` 能启动且找到 `/opt/chronos/libs/libchronos.so`。

## Phase 4 — 性能、生态与平台扩展

| 方向 | 状态 | 说明 |
| --- | --- | --- |
| 设备选择（CPU/GPU/AUTO、device index） | 计划中 | C ABI 已有 `device_index`，UI/CLI 尚未暴露 |
| 推理性能优化（tile 尺寸自适应、预取、内存复用） | 计划中 | 先有基准再优化，禁止编造 benchmark 数字 |
| 视频/序列帧处理 | 计划中 | 当前只处理静态图；视频需解码、时序一致性、音频保留策略 |
| macOS 构建 | 计划中 | 需要 Cocoa/WebKit 前端与 IOPMAssertion 抑制；CI 无 macOS runner |
| 自定义模型导入 | 计划中 | `Store.Import` 已具备基础；UI 导入流程未做 |
| 模型预设（人像/动漫/扫描件一键参数） | 计划中 | 依赖更多真实模型调参数据 |
| 性能基准套件（可复现、标注硬件） | 计划中 | 只在真实硬件上生成并注明配置，不写进营销文案 |

**Phase 4 验收条件**：性能报告可在固定硬件上重复；至少一个 macOS 构建；
新增模型不需要改 Go/前端代码（只改 registry）。

---

## 真实限制（不粉饰）

1. **OpenVINO 不是默认开启**：`-DCHRONOS_WITH_OPENVINO=ON` 需要额外构建；
   官方安装包应带 OpenVINO，但用户从源码构建时默认只有 stub/纯 Go 回退。
2. **不带 `chronos_native` 的构建无法加载 AI 模型**：`chronos info` 会显示
   `go-lanczos`，这是契约 §4 的设计取舍（保证无工具链环境可 `go test ./...`）。
3. **模型需自行下载**：仓库不打包权重（体积 + 许可证），registry 只提供 URL；
   ONNX 可直接加载，但推荐转 IR FP16 以获得更快冷启动和更小体积。
4. **不支持 WebP/AVIF 输入**（ADR-001 的代价）；需要时请先转 PNG/JPEG。
5. **无 macOS 构建**：`internal/inhibit` 在该平台返回 unsupported；Wails macOS
   前端也未接入。
6. **抑制不是绝对的**：手动锁屏、组策略、部分 Wayland 合成器行为不受应用控制，
   详见 `docs/INHIBIT.md`。
7. **CI 不做真实模型推理**：只验证结构、尺寸、错误码与冒烟链路；模型画质/速度
   结论必须来自本机实测并注明硬件。
8. **无自动更新、无遥测**；日志只在本机。

## 变更记录

| 日期 | 变更 |
| --- | --- |
| 2026-11-08 | 初版：Phase 1–4 状态、验收条件、真实限制 |
