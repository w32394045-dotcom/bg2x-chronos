/**
 * Chronos 跨语言接口契约 v1 的 TypeScript 镜像。
 *
 * 单一事实来源：docs/CONTRACTS.md §3.2 / §3.3 / §5。
 * 字段名与 Go struct 的 json tag 逐字对应（camelCase）。字段只增不删：
 * 新增字段在 UI 侧一律按可选处理，缺失时使用安全默认值。
 */

/** 后端 id 字符串：openvino | stub | go-lanczos（契约 §1 / §3.2）。 */
export type EngineBackend = 'openvino' | 'stub' | 'go-lanczos' | (string & {})

/** 模型类别（契约 §3.2 ModelInfo.kind）。 */
export type ModelKind = 'photo' | 'anime' | 'compressed' | 'general' | (string & {})

/** 进度事件种类（契约 §3.2 ProgressEvent.kind）。 */
export type ProgressKind =
  | 'queued'
  | 'started'
  | 'progress'
  | 'done'
  | 'failed'
  | 'cancelled'
  | (string & {})

/** 日志级别（契约 §3.2 LogEvent.level）。 */
export type LogLevel = 'debug' | 'info' | 'warn' | 'error' | (string & {})

export interface EngineInfo {
  /** "ort" | "openvino" | "stub" | "go-lanczos" */
  backend: string
  /** 执行后端："CPU" | "DML" | "CUDA" | ""（契约 v3） */
  ep?: string
  /** "GPU" | "CPU" | "n/a" */
  device: string
  /** 显卡/CPU 型号，可为空 */
  deviceName?: string
  detail: string
  /** 想用 GPU 但降级时的真实原因 */
  fallbackReason?: string
  /** C++ ABI 版本，当前为 1 */
  abiVersion: number
}

export interface AppInfo {
  version: string
  commit: string
  buildTime: string
  goVersion: string
  /** 例如 "windows/amd64" */
  platform: string
  engine: EngineInfo
  dataDir: string
  modelDir: string
  outputDir: string
  /** 硬上限 8 */
  maxConcurrency: number
  /** chronos 原生库是否加载成功 */
  nativeLoaded: boolean
  /**
   * 运行模式（契约 §3.2 新增）：
   * - `desktop`：Wails webview 内嵌运行
   * - `browser`：由 `chronos serve` 的本地 HTTP 服务提供页面
   * 旧二进制可能不返回该字段；前端按实际 transport 兜底（§6 字段只增不删）。
   */
  mode?: 'desktop' | 'browser' | (string & {})
  /** Go 是否提供 ListDirectory —— 浏览器模式的服务端路径选择器依赖它 */
  browseSupported?: boolean
}

/** 服务端目录枚举的一行（契约 §3.2 DirListing.entries）。 */
export interface DirEntry {
  name: string
  /** 绝对路径，可直接回传给 Go */
  path: string
  isDir: boolean
  /** 字节数；目录为 0 */
  size: number
  /** Go 侧识别的图片扩展名（png / jpg / jpeg / webp / bmp / ...） */
  isImage: boolean
}

/** ListDirectory 返回值：浏览器模式下 PathPicker 的唯一数据源。 */
export interface DirListing {
  /** 实际枚举的目录（空入参时由 Go 解析为 home 目录） */
  path: string
  /** 上一级目录；已到根时为空串 */
  parent: string
  entries: DirEntry[]
}

export interface ModelInfo {
  id: string
  name: string
  /** 模型原生倍率 */
  scale: number
  /** "photo" | "anime" | "compressed" | "general" */
  kind: string
  description: string
  sizeMb: number
  installed: boolean
  path: string
  /** 首选下载源 */
  url: string
  /** v0.3："registry" | "imported" */
  origin?: string
  /** 装完自检是否通过 */
  verified?: boolean
  license?: string
  arch?: string
  /** 是否已有 model.json sidecar */
  sidecar?: boolean
  /** 自检失败原因 */
  failReason?: string
}

/** 下载进度（chronos:model 事件与 PullModel 返回值）。 */
export interface ModelPullState {
  modelId: string
  /** queued|resolving|downloading|verifying|installing|selftest|done|failed|cancelled */
  phase: string
  done: number
  /** -1 表示服务器未给长度 */
  total: number
  error: string
}

export interface SelfTestCheck {
  /** load|size|color|seam|perf */
  name: string
  ok: boolean
  detail: string
}

export interface ModelSelfTest {
  ok: boolean
  checks: SelfTestCheck[]
  diagnostics: string
}

export interface TensorShape {
  name: string
  /** -1 表示动态维度 */
  dims: number[]
  dtype: string
}

export interface ModelDiagnostics {
  modelId: string
  opset: number
  ops: string[]
  inputs: TensorShape[]
  outputs: TensorShape[]
  warnings: string[]
  error: string
}

export interface PullModelRequest {
  modelId: string
  mirror: string
}

export interface ImportModelRequest {
  path: string
  name: string
  autoDetect: boolean
}

/** 可选运行时组件（P3：Windows 的 DirectML 运行时需下载）。 */
export interface GpuRuntimeState {
  flavor: string
  installed: boolean
  dir: string
  installable: boolean
  version: string
  sizeMb: number
  /** 实验性运行时（Linux 的 CUDA/OpenVINO，需要真机复验） */
  experimental?: boolean
}

/** 加速视图：Active 是实际在用的，其余是这台机器能用的。 */
export interface AccelStatus {
  /** "auto" | "cpu" */
  mode: string
  active: EngineInfo
  available: string[]
  gpuUsable: boolean
  fallbackReason: string
  /** GPU 运行时组件的状态（可能不存在于旧后端） */
  gpuRuntime?: GpuRuntimeState
  /** "" | resolving | downloading | installing | done | failed */
  installPhase?: string
  /** 当前下载的组件包名（onnxruntime-directml / directml） */
  installPart?: string
  installDone?: number
  installTotal?: number
  installError?: string
  /** 安装完成后需要重启应用才会真正启用 GPU */
  restartRequired?: boolean
  /** 自检失败后已自动关闭 */
  runtimeDisabled?: boolean
  verifyError?: string
}

export interface InhibitRequest {
  /** 阻止系统睡眠 */
  sleep: boolean
  /** 阻止关闭显示器 */
  display: boolean
  /** 阻止空闲锁屏（Windows 上无法阻止手动 Win+L） */
  lock: boolean
  /** 任务结束后自动释放 */
  autoRelease: boolean
}

export interface InhibitStatus {
  active: boolean
  sleep: boolean
  display: boolean
  lock: boolean
  sleepSupported: boolean
  displaySupported: boolean
  lockSupported: boolean
  /** "windows-execution-state" | "linux-logind+screen-saver" | "unsupported" | ... */
  driver: string
  detail: string
  /** 0 = 未激活；否则为 unix ms 起始时刻 */
  sinceMs: number
}

export interface BatchRequest {
  files: string[]
  /** "" => stub 引擎（无模型） */
  modelId: string
  /** 可选，直接指定本地 .xml/.onnx */
  modelPath: string
  scale: number
  outputDir: string
  concurrency: number
  tileSize: number
  overlap: number
  threads: number
  fast: boolean
  /** 非空且任一位为真 => 任务执行期间保持唤醒 */
  inhibit: InhibitRequest | null
}

export interface BatchResult {
  jobId: string
  queued: number
  /** 被跳过的输入（不存在 / 非图片 / 已在队列中的重复路径） */
  skipped: string[]
}

export interface ProgressEvent {
  jobId: string
  taskId: string
  file: string
  /** queued|started|progress|done|failed|cancelled */
  kind: string
  /** 0-based，队列序号 */
  index: number
  /** tile 进度 */
  done: number
  total: number
  output: string
  width: number
  height: number
  error: string
  backend: string
  elapsedMs: number
}

export interface JobSummary {
  jobId: string
  total: number
  completed: number
  failed: number
  cancelled: boolean
  elapsedMs: number
}

export interface LogEvent {
  /** debug|info|warn|error */
  level: string
  msg: string
  timeMs: number
}
