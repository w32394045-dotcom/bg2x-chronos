# Project Chronos — 跨语言接口契约 (v3)

> **状态：冻结。** 本文件是前端 / Go / C++ 三层之间唯一的接口事实来源。
> 改动任何签名前必须同步更新本文件与三个实现，并通知 Lead。
> 代码仓库：`w32394045-dotcom/bg2x-chronos`
> v3 只做**增量**：既有签名与语义不变，新增内容见 §8。

## 0. 分层与数据流

```
Vue 3 UI ──Wails bindings (JSON)──▶ Go 调度层 ──CGO (chronos.h)──▶ C++ 算力层
   ▲  └─HTTP + SSE (浏览器模式)──────┘
   └──────── Wails Events / SSE ─────┘
```

铁律（来自架构设计书，全部实现都遵守）：

1. **跨语言只传文件路径**，绝不传像素 buffer。图像读写在 C++ 内部完成。
2. C++ 任何异常都不得越过 `extern "C"` 边界；每个入口 `try/catch(...)`。
3. C++ 不依赖 OpenCV（ADR-001）：图像 IO 用 vendored `stb_image`，推理张量由所选运行时（ORT/OpenVINO）持有。
4. C++ 侧 `chronos_session` 非线程安全；**每个 Go worker 独占一个 session**。
5. 并发上限硬编码 8（配置层 + UI 双重约束）。
6. 浏览器模式（§7）复用**同一份** `internal/uiapp.Core`，不允许出现两套业务逻辑。
7. 推理运行时的加载是**运行时**行为（ADR-003）：C++ 用动态加载解析 ONNX Runtime，构建期不得依赖 ORT 的链接库。

## 1. C++ ⇄ Go：`native/include/chronos/chronos.h`

头文件逐字节冻结，实现必须匹配。要点：

| API | 语义 |
| --- | --- |
| `chronos_abi_version()` | 返回 `CHRONOS_ABI_VERSION`(1)，Go 启动自检 |
| `chronos_session_create(model_path, scale, opts, err, err_len)` | `model_path` 为空 ⇒ stub（Lanczos）后端；非空 ⇒ 依次尝试 ORT（动态加载）与 OpenVINO（编译期可选），全不可用返回 `NULL` 并写同时说明两条路径的 err |
| `chronos_process(session, in, out, &w, &h, err, err_len)` | 读图→切片→推理→羽化拼接→落盘，返回 0 / 负错误码 |
| `chronos_session_progress(session, &done, &total)` | 原子读取当前图片的 tile 进度，Go 侧 50ms 轮询 |
| `chronos_session_cancel(session)` | 置取消位；pipeline 在 tile 边界检查并尽快返回 `CHRONOS_ERR_CANCELLED` |

错误码（三端共用，Go 映射为 sentinel error）：

```
 0 OK | -1 IO | -2 MODEL | -3 INFER | -4 PARAM | -5 BACKEND | -6 CANCELLED | -99 UNKNOWN
```

后端 id：`0 NONE | 1 STUB | 2 OPENVINO | 3 ORT`；`chronos_backend_name` 返回
`none` / `stub` / `openvino` / `ort`。
`chronos_options.struct_size` 必须为 `sizeof(chronos_options)`；为 0 时按默认值（tile 256 / overlap 16 / threads=hw / device 0 / flags 0）。
`CHRONOS_FLAG_PRESERVE_ALPHA` 由 Go 侧始终设置（PNG 输出保留透明通道）。

### 1.1 运行时能力探测（v3 新增，只增不改）

```c
/* 运行时能否加载 ONNX Runtime（动态加载，与构建方式无关）。1 = 可加载。 */
int chronos_ort_available(void);

/* 写 e.g. "1.24.4"; 返回长度，未加载时返回 0。 */
int chronos_ort_version(char* buf, int buf_len);

/*
 * JSON 能力报告（实现见 docs/PLAN-v0.3.md §5）。返回写入长度，失败返回负错误码。
 * 报告内容只增不删；前端对缺字段做可选处理。
 */
int chronos_backend_probe(char* buf, int buf_len);
```

`chronos_backend_probe` 报告形状：

```json
{
  "cpus": 16,
  "ort": { "available": true, "version": "1.24.4", "providers": ["CPU", "DML"] },
  "openvino": { "available": false },
  "model": { "loaded": true, "sidecarFound": true, "tileAlign": 8, "fixedSize": 0, "scale": 4 },
  "device": { "selected": "CPU", "name": "", "fallbackReason": "" },
  "warnings": []
}
```

## 2. 算力层内部契约（C++）

- 切片：`tile = 256`，`overlap = 16`，`step = tile - overlap = 240`；输出画布 `(w*scale, h*scale)`。
- 每个 tile 取 `tile×tile` 输入区域（越界用边缘复制），推理后得到 `tile*scale` 的 patch。
- 羽化：每维权重 = 两端升线性渐变（1 LSB 下限），写回时**权重累加再归一化**；常量图必须逐位不变（已成测试）。
- 进度：`done/total` 以 tile 数计，原子自增；取消标志在整个 session 生命周期内保持（粘性）。
- stub 后端支持 `scale ∈ {1,2,3,4}`；ORT/OpenVINO 后端以模型原生 scale 为准（请求 scale 仅校验）。
- **模型对齐**：`tileAlign > 0` 时实际 tile 向上取整到该倍数；`fixedSize > 0` 时实际 tile 固定为该值
  （这两个值来自 sidecar，见 §8.1）。
- **输出裁剪**：`crop > 0` 时每个 patch 先从四周裁掉 `crop` 像素，再进入羽化拼接。

## 3. Go ⇄ 前端：App 方法

前端在桌面模式通过 `window.go.main.App.<Method>(...)` 调用；浏览器模式通过
`POST /api/<Method>`（§7）。事件名与 payload 两种模式完全一致。

### 3.1 方法

```go
GetAppInfo() AppInfo
ListModels() []ModelInfo
RefreshModels() []ModelInfo
PullModel(req PullModelRequest) (ModelPullState, error)   // v3：启动下载（异步），立即返回
CancelPull(modelID string) bool                           // v3
RemoveModel(modelID string) error                         // v3：删除 <modelDir>/<id>/
ImportModel(req ImportModelRequest) (ModelInfo, error)    // v3：外部 .onnx / 目录导入
SelfTestModel(modelID string) (ModelSelfTest, error)      // v3：体检 + 试跑 + 拼接不变性
GetModelDiagnostics(modelID string) ModelDiagnostics      // v3：可复制的诊断文本
GetAccelStatus() AccelStatus                              // v3：后端/EP/设备/降级原因
SetAccelMode(mode string) (AccelStatus, error)            // v3："auto" | "cpu"
InstallAccelRuntime() (AccelStatus, error)                // v3/P3：下载并安装 GPU 运行时（异步，进度走 chronos:accel + GetAccelStatus）
UpscaleBatch(req BatchRequest) (BatchResult, error)
CancelJob(jobID string) bool
SetConcurrency(n int) error                 // 1..8
GetConcurrency() int
SetInhibit(req InhibitRequest) (InhibitStatus, error)
GetInhibitStatus() InhibitStatus
PickImages() []string                       // 浏览器模式返回空
PickOutputDir() string                      // 浏览器模式返回空
PickModelFile() string                      // v3：桌面选模型文件；浏览器模式返回空
Reveal(path string) error
OpenDataDir() error
ListDirectory(path string) (DirListing, error)   // 浏览器模式服务器端文件选择
```

`ListDirectory`：空 path 表示用户主目录；只返回子目录与图片文件，目录在前、忽略隐藏文件。

```go
type DirEntry struct {
    Name    string `json:"name"`
    Path    string `json:"path"`
    IsDir   bool   `json:"isDir"`
    Size    int64  `json:"size"`
    IsImage bool   `json:"isImage"`
}
type DirListing struct {
    Path    string     `json:"path"`
    Parent  string     `json:"parent"`
    Entries []DirEntry `json:"entries"`
}
```

### 3.2 数据结构（JSON tag 即前端字段名，camelCase）

```go
type AppInfo struct {
    Version         string      `json:"version"`
    Commit          string      `json:"commit"`
    BuildTime       string      `json:"buildTime"`
    GoVersion       string      `json:"goVersion"`
    Platform        string      `json:"platform"`
    Engine          EngineInfo  `json:"engine"`
    DataDir         string      `json:"dataDir"`
    ModelDir        string      `json:"modelDir"`
    OutputDir       string      `json:"outputDir"`
    MaxConcurrency  int         `json:"maxConcurrency"`
    NativeLoaded    bool        `json:"nativeLoaded"`
    Mode            string      `json:"mode"`            // "desktop" | "browser"
    BrowseSupported bool        `json:"browseSupported"` // browser => true
}

// v3：字段只增不删；旧前端忽略新字段即可。
type EngineInfo struct {
    Backend        string `json:"backend"`        // "ort" | "openvino" | "stub" | "go-lanczos"
    EP             string `json:"ep"`             // "CPU" | "DML" | "CUDA" | "" (v3)
    Device         string `json:"device"`         // "GPU" | "CPU" | "n/a"
    DeviceName     string `json:"deviceName"`     // 显卡/CPU 型号，可为空 (v3)
    Detail         string `json:"detail"`
    FallbackReason string `json:"fallbackReason"` // 想用 GPU 但降级的真实原因 (v3)
    ABIVersion     int    `json:"abiVersion"`
    Native         bool   `json:"native"`
}

type AccelStatus struct {           // v3
    Mode           string   `json:"mode"`     // "auto" | "cpu"
    Active         EngineInfo `json:"active"`
    Available      []string `json:"available"` // 例如 ["CPU","DML"]
    GpuUsable      bool     `json:"gpuUsable"`
    FallbackReason string   `json:"fallbackReason"`
    // P3：可选 GPU 运行时组件（Windows/DirectML 需下载，Linux 暂不支持 GPU）
    GpuRuntime      GpuRuntimeState `json:"gpuRuntime"`
    InstallPhase    string          `json:"installPhase"`  // "" | resolving|downloading|installing|done|failed
    InstallDone     int64           `json:"installDone"`
    InstallTotal    int64           `json:"installTotal"`
    InstallError    string          `json:"installError"`
    RestartRequired bool            `json:"restartRequired"` // 安装完成后需重启才能激活
}

type GpuRuntimeState struct {       // P3
    Flavor      string  `json:"flavor"`      // "directml"
    Installed   bool    `json:"installed"`
    Dir         string  `json:"dir"`
    Installable bool    `json:"installable"`
    Version     string  `json:"version"`
    SizeMB      float64 `json:"sizeMb"`
}

type ModelInfo struct {
    ID          string  `json:"id"`
    Name        string  `json:"name"`
    Scale       int     `json:"scale"`
    Kind        string  `json:"kind"` // photo | anime | compressed | general
    Description string  `json:"description"`
    SizeMB      float64 `json:"sizeMb"`
    Installed   bool    `json:"installed"`
    Path        string  `json:"path"`
    URL         string  `json:"url"`
    // v3
    Origin      string  `json:"origin"`      // "registry" | "imported"
    Verified    bool    `json:"verified"`    // 装完自检通过
    License     string  `json:"license"`
    Arch        string  `json:"arch"`
    Sidecar     bool    `json:"sidecar"`     // 是否已有 model.json
    FailReason  string  `json:"failReason"`  // 自检失败原因
}

type PullModelRequest struct {      // v3
    ModelID string `json:"modelId"`
    Mirror  string `json:"mirror"`   // "" = 自动（registry → fallback → 镜像前缀）
}

type ModelPullState struct {        // v3
    ModelID string `json:"modelId"`
    Phase   string `json:"phase"`    // queued|resolving|downloading|verifying|installing|selftest|done|failed|cancelled
    Done    int64  `json:"done"`
    Total   int64  `json:"total"`    // -1 = 服务器未给 Content-Length
    Error   string `json:"error"`
}

type ImportModelRequest struct {    // v3
    Path     string `json:"path"`     // 单个 .onnx 或包含 .onnx/.data 的目录
    Name     string `json:"name"`     // 可选显示名
    AutoDetect bool `json:"autoDetect"` // true = 尽量自动探测 scale/norm/crop
}

type ModelSelfTest struct {         // v3
    OK         bool     `json:"ok"`
    Checks     []SelfTestCheck `json:"checks"`
    Diagnostics string  `json:"diagnostics"` // 可直接复制给开发者的诊断文本
}

type SelfTestCheck struct {         // v3
    Name    string `json:"name"`   // import|shape|size|seam|color|perf
    OK      bool   `json:"ok"`
    Detail  string `json:"detail"`
}

type ModelDiagnostics struct {      // v3
    ModelID   string   `json:"modelId"`
    Opset     int      `json:"opset"`
    Ops       []string `json:"ops"`
    Inputs    []TensorShape `json:"inputs"`
    Outputs   []TensorShape `json:"outputs"`
    Warnings  []string `json:"warnings"`
    Error     string   `json:"error"`
}

type TensorShape struct {           // v3
    Name  string `json:"name"`
    Dims  []int  `json:"dims"`   // -1 = 动态
    Dtype string `json:"dtype"`
}

type BatchRequest struct {
    Files       []string        `json:"files"`
    ModelID     string          `json:"modelId"`
    ModelPath   string          `json:"modelPath"`
    Scale       int             `json:"scale"`
    OutputDir   string          `json:"outputDir"`
    Concurrency int             `json:"concurrency"`
    TileSize    int             `json:"tileSize"`
    Overlap     int             `json:"overlap"`
    Threads     int             `json:"threads"`
    Fast        bool            `json:"fast"`
    Inhibit     *InhibitRequest `json:"inhibit"`
}

type BatchResult struct {
    JobID   string   `json:"jobId"`
    Queued  int      `json:"queued"`
    Skipped []string `json:"skipped"`
}

type ProgressEvent struct {
    JobID     string `json:"jobId"`
    TaskID    string `json:"taskId"`
    File      string `json:"file"`
    Kind      string `json:"kind"` // queued|started|progress|done|failed|cancelled
    Index     int    `json:"index"`
    Done      int    `json:"done"`
    Total     int    `json:"total"`
    Output    string `json:"output"`
    Width     int    `json:"width"`
    Height    int    `json:"height"`
    Error     string `json:"error"`
    Backend   string `json:"backend"`
    ElapsedMs int64  `json:"elapsedMs"`
}

type JobSummary struct {
    JobID     string `json:"jobId"`
    Total     int    `json:"total"`
    Completed int    `json:"completed"`
    Failed    int    `json:"failed"`
    Cancelled bool   `json:"cancelled"`
    ElapsedMs int64  `json:"elapsedMs"`
}

type LogEvent struct {
    Level  string `json:"level"` // debug|info|warn|error
    Msg    string `json:"msg"`
    TimeMs int64  `json:"timeMs"`
}

type InhibitRequest struct {
    Sleep       bool `json:"sleep"`
    Display     bool `json:"display"`
    Lock        bool `json:"lock"`
    AutoRelease bool `json:"autoRelease"`
}

type InhibitStatus struct {
    Active           bool   `json:"active"`
    Sleep            bool   `json:"sleep"`
    Display          bool   `json:"display"`
    Lock             bool   `json:"lock"`
    SleepSupported   bool   `json:"sleepSupported"`
    DisplaySupported bool   `json:"displaySupported"`
    LockSupported    bool   `json:"lockSupported"`
    Driver           string `json:"driver"`
    Detail           string `json:"detail"`
    SinceMs          int64  `json:"sinceMs"`
}
```

### 3.3 事件

| 事件名 | payload | 频率 |
| --- | --- | --- |
| `chronos:progress` | `ProgressEvent` | 每 tile 批 / 每 150–500ms 节流 |
| `chronos:job` | `JobSummary` | 每 job 一次 |
| `chronos:log` | `LogEvent` | 按需 |
| `chronos:inhibit` | `InhibitStatus` | 状态变化时 |
| `chronos:model` | `ModelPullState` | v3：下载/自检进度，节流 ≤ 5 次/秒 |
| `chronos:accel` | `AccelStatus` | v3：加速方式或后端变化时 |

任务完成后若 `BatchRequest.Inhibit.AutoRelease` 为真，Go 侧自动释放并推送一次 `chronos:inhibit`。

### 3.4 命名与落盘规则（Go 层）

- 输出目录为空 ⇒ 与输入同目录；文件名 `<原名>_chronos_x<scale>.png`。
- 同名冲突追加 `-1`、`-2`…（直到 999）；输出统一 PNG（保留 alpha）。
- 跳过：不存在 / 非图片 / 同一 job 内重复路径；写入 `BatchResult.Skipped`。
- 模型目录布局见 §8.2；**同一模型不能被并发下载两次**（以 modelID 去重）。

## 4. Go ⇄ C++：CGO 构建契约

- 头文件：`native/include/chronos/chronos.h`；Go `#cgo CFLAGS: -I${SRCDIR}/../../native/include`。
- 链接：`-L${SRCDIR}/../../native/build -lchronos`；Linux 额外 rpath `$ORIGIN/libs`、`$ORIGIN/../lib`。
- **不链接 ONNX Runtime**：ORT 由运行期动态加载（ADR-003），因此 `-DCHRONOS_WITH_OPENVINO=ON`
  与 ORT 可用性是两个独立维度。
- 构建标签：**仅当** `cgo && chronos_native` 时编译 CGO 实现；否则纯 Go Lanczos 回退。
- 运行时库：Windows 把 `chronos.dll` 放 exe 同目录（或 PATH）；Linux 用 rpath / `LD_LIBRARY_PATH`。

```bash
cmake -S native -B native/build -G Ninja -DCMAKE_BUILD_TYPE=Release
cmake --build native/build
ctest --test-dir native/build --output-on-failure
go test -tags chronos_native ./internal/engine/...
```

## 5. 前端传输契约

```ts
export interface Api {
  GetAppInfo(): Promise<AppInfo>
  ListModels(): Promise<ModelInfo[]>
  RefreshModels(): Promise<ModelInfo[]>
  PullModel(req: PullModelRequest): Promise<ModelPullState>
  CancelPull(modelId: string): Promise<boolean>
  RemoveModel(modelId: string): Promise<void>
  ImportModel(req: ImportModelRequest): Promise<ModelInfo>
  SelfTestModel(modelId: string): Promise<ModelSelfTest>
  GetModelDiagnostics(modelId: string): Promise<ModelDiagnostics>
  GetAccelStatus(): Promise<AccelStatus>
  SetAccelMode(mode: string): Promise<AccelStatus>
  InstallAccelRuntime(): Promise<AccelStatus>
  UpscaleBatch(req: BatchRequest): Promise<BatchResult>
  CancelJob(jobId: string): Promise<boolean>
  SetConcurrency(n: number): Promise<void>
  GetConcurrency(): Promise<number>
  SetInhibit(req: InhibitRequest): Promise<InhibitStatus>
  GetInhibitStatus(): Promise<InhibitStatus>
  PickImages(): Promise<string[]>
  PickOutputDir(): Promise<string>
  PickModelFile(): Promise<string>
  Reveal(path: string): Promise<void>
  OpenDataDir(): Promise<void>
  ListDirectory(path: string): Promise<DirListing>
}
```

传输选择顺序：`window.go.main.App`（Wails）→ `window.__CHRONOS_HTTP__ === true`（HTTP/SSE）→ mock（纯浏览器预览）。
桌面模式的对话框走 Wails 原生 API；浏览器模式用 `ListDirectory` 的服务器端路径选择器。

## 6. 版本与兼容

- C ABI 版本 1；Go 启动时 `chronos_abi_version()` 不符即降级并写日志。
- v3 的 C 侧新增只有**新符号**与 `CHRONOS_BACKEND_ORT` 常量，既有函数签名与结构体布局不变；
  `chronos_options.reserved[4]` 保留，未来扩展不得改变既有字段语义。
- 前端只依赖 JSON 字段名，字段只增不删；新增字段前端做可选处理。
- 模型 registry：`internal/models/registry.json`（单源，Go `embed`）；schema v2 见 §8.2。
- sidecar schema 见 §8.1，**Go 与 C++ 共用同一份 JSON 字段名**，两端测试引用同一 fixture。

## 7. 浏览器模式（无 WebView2 / WebKit 时）

桌面窗口不可用（Windows 无 WebView2、Linux 无 `DISPLAY`/`WAYLAND_DISPLAY`，或
`CHRONOS_BROWSER=1` / `--browser`）时，`internal/browser` 用同一份 Core 提供服务：

- `GET /`：返回注入 `<script>window.__CHRONOS_HTTP__=true;</script>` 的 index.html，并下发
  `HttpOnly + SameSite=Strict` 随机会话 cookie。
- `POST /api/<Method>`：body `{"args":[...]}`，响应 `{"result":...}` 或 `{"error":"..."}`；
  方法/参数/JSON 与 §3 完全一致。`/api/*` 与 `/events` 校验 cookie，且 `Origin` 必须为本地地址
  （防 CSRF；无 Origin 的 curl 请求允许）。
- `GET /events`：SSE，每条 `data: {"name":"chronos:progress","data":{...}}`。
- 只监听 `127.0.0.1`（默认随机端口）；`chronos serve` 打印 `{"mode":"browser","url":...}`。
- CLI 的资源解析顺序：`--assets` → `CHRONOS_ASSETS` → `<exeDir>/web` → `<exeDir>/frontend/dist`
  → `./frontend/dist` → `./web`。
- 打包：`chronos-<ver>-linux-<arch>-web.tar.gz` 内含 CLI、`libs/libchronos.so`、`web/`、`chronos-web` 启动器。

## 8. v0.3 新增：SR Contract 与模型布局

详细背景、验收标准与排期见 `docs/PLAN-v0.3.md`；此处只冻结接口可见部分。

### 8.1 sidecar：`<modelDir>/<id>/model.json`

```json
{
  "inputs":  [{ "name": "input", "source": "image" }],
  "outputs": [{ "name": "", "scale": 4, "crop": 0 }],
  "preprocess": { "norm": "0-1", "channels": 3, "mean": [], "std": [] },
  "constraints": { "tileAlign": 1, "fixedSize": 0, "batch": 1, "dtype": "fp32" },
  "meta": { "license": "BSD-3-Clause", "arch": "rrdb", "notes": "" }
}
```

- `inputs[].source`：`"image"`（该输入接图像）或 `"constant:<float>"`（常量填充，P5 起支持）。
- `outputs[0].name` 空 ⇒ 取模型第一个 4D 输出。
- `outputs[0].scale`：**已写入的 sidecar 必须为正**（Go 侧 `Validate` 拒绝 `scale <= 0`）；
  仅当 sidecar 缺失或未给出 scale 时，C++ 端才按输出/输入空间比例推导，推导不出再用请求的 scale。
- `preprocess.norm`：`0-1` / `0-255` / `-1-1`；`channels` ∈ `1|3|4`。
- `constraints.fixedSize > 0` ⇒ tile 固定为该值；`tileAlign > 0` ⇒ tile 向上取整到该倍数。
- 缺省：norm `0-1`、channels 3、crop 0、tileAlign 1、batch 1、dtype `fp32`。

### 8.2 模型存储布局 v2

```
<modelDir>/
  <id>/
    model.onnx            # 必需
    model.onnx.data       # 可选（外部权重，ORT 原生支持）
    model.json            # sidecar
```

- 旧的扁平 `models/<id>.onnx|.xml` 在首次访问时原子迁移到新布局（幂等）。
- registry v2 每项新增：`arch`、`license`、`tileAlign`、`norm`、`sha256`（必填）、`extraFiles`（可选）。
- `sha256` 是下载安装的强校验；缺失即视为 registry 无效（CI 检查）。

### 8.3 SR Contract（必须逐条满足）

| 项 | 要求 |
| --- | --- |
| 输入 | 恰好 1 个图像张量，4D NCHW，N=1，C=3（C=1/4 由适配层转换） |
| 输入 dtype | `float32`（`float16` 允许） |
| 输出 | ≥1 个；`outputs[0]` 指向的张量必须是 4D NCHW，N=1，C=3 |
| 倍数 | 整数 1–8（1 = 去噪/去块/修复） |
| 算子域 | 标准 ONNX 域；`com.microsoft` 需白名单 |
| 文件 | `.onnx`（可带 `.onnx.data`），≤ 2GB |
| 不支持 | 多输入图像、多输出里无 4D、LLM/检测分割、int8、`.pb/.tflite/.param` |

## 9. 变更记录

| 日期 | 版本 | 变更 |
| --- | --- | --- |
| 2026-11-08 | v2 | 浏览器模式（HTTP+SSE）、`ListDirectory`、`mode`/`browseSupported`、Web 打包 |
| 2026-11-08 | v3 | ORT 后端 id 3 与能力探测；模型一条龙/导入/自检 RPC；`AccelStatus`；sidecar 与布局 v2；SR Contract |
