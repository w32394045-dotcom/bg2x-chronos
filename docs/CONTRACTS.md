# Project Chronos — 跨语言接口契约 (v1)

> **状态：冻结。** 本文件是前端 / Go / C++ 三层之间唯一的接口事实来源。
> 改动任何签名前必须同步更新本文件与三个实现，并通知 Lead。
> 代码仓库：`w32394045-dotcom/bg2x-chronos`

## 0. 分层与数据流

```
Vue 3 UI  ──Wails bindings (JSON RPC)──▶  Go 调度层  ──CGO (chronos.h)──▶  C++ 算力层
   ▲                                        │
   └──────── Wails Events (JSON) ───────────┘
```

铁律（来自架构设计书第七节，全部实现都遵守）：

1. **跨语言只传文件路径**，绝不传像素 buffer。图像读写在 C++ 内部完成。
2. C++ 任何异常都不得越过 `extern "C"` 边界；每个入口 `try/catch(...)`。
3. C++ 不依赖 OpenCV（ADR-001）：图像 IO 用 vendored `stb_image`，推理张量用 OpenVINO 原生 `ov::Tensor`。
4. C++ 侧 `chronos_session` 非线程安全；**每个 Go worker 独占一个 session**。
5. 并发上限硬编码 8（配置层 + UI 双重约束）。

## 1. C++ ⇄ Go：`native/include/chronos/chronos.h`

完整头文件已冻结，实现必须逐字节匹配。要点：

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

后端 id：`0 NONE | 1 STUB | 2 OPENVINO`。

`chronos_options.struct_size` 必须为 `sizeof(chronos_options)`；为 0 时按默认值（tile 256 / overlap 16 / threads=hw / device 0 / flags 0）。

## 2. 算力层内部契约（C++）

- 切片：`tile = 256`，`overlap = 16`，`step = tile - overlap = 240`。输出画布为 `(w*scale, h*scale)`。
- 每个 tile 在输入图上取 `tile×tile` 区域（越界用边缘复制 / replicate padding），推理后得到 `tile*scale` 的 patch。
- 羽化：每维权重 = 两端线性渐变（端点为 `1/tile` 级小权重，避免 0 除），写回大图时按**权重累加再归一化**，因此重叠区是加权平均而非硬拼接；重叠区中两个线性斜坡之和为常数，保证无接缝。
- 进度：`done/total` 以 tile 数计，原子自增。
- stub 后端必须支持 `scale ∈ {1,2,3,4}`，OpenVINO 后端以模型原生 scale 为准（请求 scale 仅用于校验）。

## 3. Go ⇄ 前端：App 方法

前端统一通过 `window.go.main.App.<Method>(...)`（Promise）调用，通过 `window.runtime.EventsOn("<event>", cb)` 订阅事件。
前端**不提交自动生成的 wailsjs bindings**，统一走 `src/lib/api.ts` 的薄封装 + 类型定义（见 §5），mock 模式保证 `vite dev` 可独立预览。

### 3.1 方法

```go
GetAppInfo() AppInfo
ListModels() []ModelInfo                    // 内置 registry + 本地已下载状态
RefreshModels() []ModelInfo                 // 重新扫描模型目录
UpscaleBatch(req BatchRequest) (BatchResult, error)
CancelJob(jobID string) bool
SetConcurrency(n int) error                 // 1..8，越界返回 error
GetConcurrency() int
SetInhibit(req InhibitRequest) (InhibitStatus, error)
GetInhibitStatus() InhibitStatus
PickImages() []string
PickOutputDir() string
Reveal(path string) error
OpenDataDir() error
```

### 3.2 数据结构（JSON tag 即前端字段名，camelCase）

```go
type AppInfo struct {
    Version       string      `json:"version"`        // e.g. "0.1.0"
    Commit        string      `json:"commit"`
    BuildTime     string      `json:"buildTime"`
    GoVersion     string      `json:"goVersion"`
    Platform      string      `json:"platform"`       // "windows/amd64"
    Engine        EngineInfo  `json:"engine"`
    DataDir       string      `json:"dataDir"`
    ModelDir      string      `json:"modelDir"`
    OutputDir     string      `json:"outputDir"`
    MaxConcurrency int        `json:"maxConcurrency"` // 8
    NativeLoaded  bool        `json:"nativeLoaded"`
}

type EngineInfo struct {
    Backend  string `json:"backend"`   // "openvino" | "stub" | "go-lanczos"
    Device   string `json:"device"`    // "GPU" | "CPU" | "n/a"
    Detail   string `json:"detail"`
    ABIVersion int  `json:"abiVersion"`
}

type ModelInfo struct {
    ID          string  `json:"id"`
    Name        string  `json:"name"`
    Scale       int     `json:"scale"`
    Kind        string  `json:"kind"`        // "photo" | "anime" | "compressed" | "general"
    Description string  `json:"description"`
    SizeMB      float64 `json:"sizeMb"`
    Installed   bool    `json:"installed"`
    Path        string  `json:"path"`
    URL         string  `json:"url"`         // 首选下载源
}

type BatchRequest struct {
    Files        []string        `json:"files"`
    ModelID      string          `json:"modelId"`      // "" => stub 引擎（无模型）
    ModelPath    string          `json:"modelPath"`    // 可选，直接指定本地 .xml/.onnx
    Scale        int             `json:"scale"`
    OutputDir    string          `json:"outputDir"`
    Concurrency  int             `json:"concurrency"`
    TileSize     int             `json:"tileSize"`
    Overlap      int             `json:"overlap"`
    Threads      int             `json:"threads"`
    Fast         bool            `json:"fast"`
    Inhibit      *InhibitRequest `json:"inhibit"`      // 非空且任一位为真 => 任务执行期间保持唤醒
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
    Kind      string `json:"kind"`     // queued|started|progress|done|failed|cancelled
    Index     int    `json:"index"`    // 0-based，队列序号
    Done      int    `json:"done"`     // tile 进度
    Total     int    `json:"total"`
    Output    string `json:"output"`
    Width     int    `json:"width"`
    Height    int    `json:"height"`
    Error     string `json:"error"`
    Backend   string `json:"backend"`
    ElapsedMs int64  `json:"elapsedMs"`
}

type JobSummary struct { // 一个 job 结束（全部 done/failed/cancelled）
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
    Sleep      bool `json:"sleep"`      // 阻止系统睡眠
    Display    bool `json:"display"`    // 阻止关闭显示器
    Lock       bool `json:"lock"`       // 阻止空闲锁屏（Windows 上无法阻止手动 Win+L）
    AutoRelease bool `json:"autoRelease"` // 任务结束后自动释放
}

type InhibitStatus struct {
    Active           bool   `json:"active"`
    Sleep            bool   `json:"sleep"`            // 当前是否持有
    Display          bool   `json:"display"`
    Lock             bool   `json:"lock"`
    SleepSupported   bool   `json:"sleepSupported"`
    DisplaySupported bool   `json:"displaySupported"`
    LockSupported    bool   `json:"lockSupported"`
    Driver           string `json:"driver"`           // "windows-execution-state" | "linux-logind+screen-saver" | "unsupported" | ...
    Detail           string `json:"detail"`
    SinceMs          int64  `json:"sinceMs"`          // 0 = 未激活；否则为 unix ms 起始时刻
}
```

### 3.3 事件

| 事件名 | payload | 频率 |
| --- | --- | --- |
| `chronos:progress` | `ProgressEvent` | 每 tile 批/每 500ms 节流 |
| `chronos:job` | `JobSummary` | 每 job 一次 |
| `chronos:log` | `LogEvent` | 按需 |
| `chronos:inhibit` | `InhibitStatus` | 状态变化时 |

任务完成后若 `BatchRequest.Inhibit.AutoRelease` 为真，Go 侧自动 `SetInhibit({})` 并推送一次 `chronos:inhibit`。

### 3.4 命名与落盘规则（Go 层）

- 输出目录为空 ⇒ 与输入同目录。
- 文件名：`<原名>_chronos_x<scale>.<ext>`，扩展名默认 `png`（APNG/JPEG 输入也统一输出 PNG 以保证无损；`Fast` 模式不改变扩展名策略）。
- 同名冲突追加 `-1`,`-2`…（直到 999，超限报错）。
- 跳过：不存在 / 非图片 / 已在队列中的重复路径；写入 `BatchResult.Skipped`。

## 4. Go ⇄ C++：CGO 构建契约

- C 头文件：`native/include/chronos/chronos.h`；Go 侧 `#cgo CFLAGS: -I${SRCDIR}/../../native/include`。
- 链接：`-L${SRCDIR}/../../native/build -lchronos`（Linux `libchronos.so`，Windows `chronos.dll` + 导入库）。
- 构建标签：**仅当** `cgo && chronos_native` 时编译 CGO 实现；否则走纯 Go Lanczos 回退实现（保证 `go test ./...` 在无工具链环境可跑）。
- 运行时库查找：Windows 将 `chronos.dll` 放在 exe 同目录；Linux 用 `rpath=$ORIGIN/libs`（CI 里设置 `LD_LIBRARY_PATH`）。
- CGO 调用统一在 `internal/engine/native_cgo.go`，包一层 `recover()` + 错误码映射。

构建命令（CI 与本地一致）：

```bash
cmake -S native -B native/build -G Ninja -DCMAKE_BUILD_TYPE=Release
cmake --build native/build
ctest --test-dir native/build --output-on-failure
go test -tags chronos_native ./internal/engine/...
```

## 5. 前端 `src/lib/api.ts` 契约

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
}
```

- `isDesktop()`：`!!(window as any).go?.main?.App`。
- 非桌面环境（`vite dev` 浏览器预览）自动进入 mock：返回示例数据、模拟 tile 进度、Inhibit 状态可用。
- 事件订阅封装 `onEvent<T>(name, handler): () => void`，并做 payload 归一化（`elapsedMs` 可能为 0）。

## 6. 版本与兼容

- ABI 版本 1；Go 启动时 `chronos_abi_version()` 不符即降级到 go-lanczos 并写日志。
- 前端只依赖 JSON 字段名，字段只增不删；新增字段前端做可选处理。
- 模型 registry：`models/registry.json`（单源），Go `embed`，前端由 `ListModels()` 获取，不重复维护列表。
