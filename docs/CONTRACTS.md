# Project Chronos — 跨语言接口契约 (v2)

> **状态：冻结。** 本文件是前端 / Go / C++ 三层之间唯一的接口事实来源。
> 改动任何签名前必须同步更新本文件与三个实现，并通知 Lead。
> 代码仓库：`w32394045-dotcom/bg2x-chronos`

## 0. 分层与数据流

```
Vue 3 UI ──Wails bindings (JSON)──▶ Go 调度层 ──CGO (chronos.h)──▶ C++ 算力层
   ▲  └─HTTP + SSE (浏览器模式)──────┘
   └──────── Wails Events / SSE ─────┘
```

铁律（来自架构设计书，全部实现都遵守）：

1. **跨语言只传文件路径**，绝不传像素 buffer。图像读写在 C++ 内部完成。
2. C++ 任何异常都不得越过 `extern "C"` 边界；每个入口 `try/catch(...)`。
3. C++ 不依赖 OpenCV（ADR-001）：图像 IO 用 vendored `stb_image`，推理张量用 OpenVINO 原生 `ov::Tensor`。
4. C++ 侧 `chronos_session` 非线程安全；**每个 Go worker 独占一个 session**。
5. 并发上限硬编码 8（配置层 + UI 双重约束）。
6. 浏览器模式（§7）复用**同一份** `internal/uiapp.Core`，不允许出现两套业务逻辑。

## 1. C++ ⇄ Go：`native/include/chronos/chronos.h`

头文件逐字节冻结，实现必须匹配。要点：

| API | 语义 |
| --- | --- |
| `chronos_abi_version()` | 返回 `CHRONOS_ABI_VERSION`(1)，Go 启动自检 |
| `chronos_session_create(model_path, scale, opts, err, err_len)` | `model_path` 为空 ⇒ stub（Lanczos）后端；非空且编译了 OpenVINO ⇒ 尝试加载 `.xml`/`.onnx`，失败返回 `NULL` 并写 err |
| `chronos_process(session, in, out, &w, &h, err, err_len)` | 读图→切片→推理→羽化拼接→落盘，返回 0 / 负错误码 |
| `chronos_session_progress(session, &done, &total)` | 原子读取当前图片的 tile 进度，Go 侧 50ms 轮询 |
| `chronos_session_cancel(session)` | 置取消位；pipeline 在 tile 边界检查并尽快返回 `CHRONOS_ERR_CANCELLED` |

错误码（三端共用，Go 映射为 sentinel error）：

```
 0 OK | -1 IO | -2 MODEL | -3 INFER | -4 PARAM | -5 BACKEND | -6 CANCELLED | -99 UNKNOWN
```

后端 id：`0 NONE | 1 STUB | 2 OPENVINO`；`chronos_backend_name` 返回 `none` / `stub` / `openvino`。
`chronos_options.struct_size` 必须为 `sizeof(chronos_options)`；为 0 时按默认值（tile 256 / overlap 16 / threads=hw / device 0 / flags 0）。
`CHRONOS_FLAG_PRESERVE_ALPHA` 由 Go 侧始终设置（PNG 输出保留透明通道）。

## 2. 算力层内部契约（C++）

- 切片：`tile = 256`，`overlap = 16`，`step = tile - overlap = 240`；输出画布 `(w*scale, h*scale)`。
- 每个 tile 取 `tile×tile` 输入区域（越界用边缘复制），推理后得到 `tile*scale` 的 patch。
- 羽化：每维权重 = 两端升线性渐变（1 LSB 下限），写回时**权重累加再归一化**；常量图必须逐位不变（已成测试）。
- 进度：`done/total` 以 tile 数计，原子自增；取消标志在整个 session 生命周期内保持（粘性）。
- stub 后端支持 `scale ∈ {1,2,3,4}`；OpenVINO 后端以模型原生 scale 为准（请求 scale 仅校验）。

## 3. Go ⇄ 前端：App 方法

前端在桌面模式通过 `window.go.main.App.<Method>(...)` 调用；浏览器模式通过
`POST /api/<Method>`（§7）。事件名与 payload 两种模式完全一致。

### 3.1 方法

```go
GetAppInfo() AppInfo
ListModels() []ModelInfo
RefreshModels() []ModelInfo
UpscaleBatch(req BatchRequest) (BatchResult, error)
CancelJob(jobID string) bool
SetConcurrency(n int) error                 // 1..8
GetConcurrency() int
SetInhibit(req InhibitRequest) (InhibitStatus, error)
GetInhibitStatus() InhibitStatus
PickImages() []string                       // 浏览器模式返回空
PickOutputDir() string                      // 浏览器模式返回空
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

type EngineInfo struct {
    Backend    string `json:"backend"`  // "openvino" | "stub" | "go-lanczos"
    Device     string `json:"device"`   // "GPU" | "CPU" | "n/a"
    Detail     string `json:"detail"`
    ABIVersion int    `json:"abiVersion"`
    Native     bool   `json:"native"`
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

任务完成后若 `BatchRequest.Inhibit.AutoRelease` 为真，Go 侧自动释放并推送一次 `chronos:inhibit`。

### 3.4 命名与落盘规则（Go 层）

- 输出目录为空 ⇒ 与输入同目录；文件名 `<原名>_chronos_x<scale>.png`。
- 同名冲突追加 `-1`、`-2`…（直到 999）；输出统一 PNG（保留 alpha）。
- 跳过：不存在 / 非图片 / 同一 job 内重复路径；写入 `BatchResult.Skipped`。

## 4. Go ⇄ C++：CGO 构建契约

- 头文件：`native/include/chronos/chronos.h`；Go `#cgo CFLAGS: -I${SRCDIR}/../../native/include`。
- 链接：`-L${SRCDIR}/../../native/build -lchronos`；Linux 额外 rpath `$ORIGIN/libs`、`$ORIGIN/../lib`。
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
  UpscaleBatch(req: BatchRequest): Promise<BatchResult>
  CancelJob(jobId: string): Promise<boolean>
  SetConcurrency(n: number): Promise<void>
  GetConcurrency(): Promise<number>
  SetInhibit(req: InhibitRequest): Promise<InhibitStatus>
  GetInhibitStatus(): Promise<InhibitStatus>
  PickImages(): Promise<string[]>
  PickOutputDir(): Promise<string>
  Reveal(path: string): Promise<void>
  OpenDataDir(): Promise<void>
  ListDirectory(path: string): Promise<DirListing>
}
```

传输选择顺序：`window.go.main.App`（Wails）→ `window.__CHRONOS_HTTP__ === true`（HTTP/SSE）→ mock（纯浏览器预览）。
桌面模式的对话框走 Wails 原生 API；浏览器模式用 `ListDirectory` 的服务器端路径选择器。

## 6. 版本与兼容

- C ABI 版本 1；Go 启动时 `chronos_abi_version()` 不符即降级并写日志。
- 前端只依赖 JSON 字段名，字段只增不删；新增字段前端做可选处理。
- 模型 registry：`internal/models/registry.json`（单源，Go `embed`）。

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
