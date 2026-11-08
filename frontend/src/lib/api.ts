/**
 * 前端唯一的后端入口（契约 §5）。
 *
 * 三种 transport，优先级：HTTP 标记 > Wails 绑定 > mock：
 * - `http`：页面由 Go `chronos serve` 提供（`window.__CHRONOS_HTTP__ === true`），
 *   走 POST /api/<Method> + SSE /events，见 `http.ts`；
 * - `wails`：`window.go.main.App.<Method>` + `window.runtime.EventsOn`（桌面 webview）；
 * - `mock`：`vite dev` / 无任何后端时，返回示例数据并模拟事件（见 `mock.ts`）。
 *
 * Wails 模式下每个方法仍逐方法回退到 mock：旧二进制缺少某个绑定时 UI 不会整体崩溃。
 */
import { getDesktopApp, getWailsRuntime, isDesktop } from './env'
import { normalizeEvent } from './events'
import { mockApi } from './mock'
import { mockSubscribe } from './eventbus'
import {
  createHttpApi,
  createHttpEventBus,
  detectTransport,
  type HttpEventBus,
  type HttpTransportOptions,
  type Transport,
} from './http'
import type {
  AccelStatus,
  AppInfo,
  BatchRequest,
  BatchResult,
  DirListing,
  ImportModelRequest,
  InhibitRequest,
  InhibitStatus,
  JobSummary,
  LogEvent,
  ModelDiagnostics,
  ModelInfo,
  ModelPullState,
  ModelSelfTest,
  ProgressEvent,
  PullModelRequest,
} from './types'

export { isDesktop }
export { detectTransport }
export type { Transport }

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
  /** 浏览器模式的服务端目录枚举（空字符串 = Go 侧 home 目录）。 */
  ListDirectory(path: string): Promise<DirListing>
  /** 桌面原生文件选择；浏览器模式返回空串（改用 ListDirectory）。 */
  PickModelFile(): Promise<string>
  /** 启动模型下载（异步），进度走 chronos:model 事件。 */
  PullModel(req: PullModelRequest): Promise<ModelPullState>
  CancelPull(modelId: string): Promise<boolean>
  RemoveModel(modelId: string): Promise<void>
  /** 导入外部 .onnx（或包含 .onnx/.data 的目录）。 */
  ImportModel(req: ImportModelRequest): Promise<ModelInfo>
  /** 装完自检：加载/尺寸/色彩/拼接不变性/耗时。 */
  SelfTestModel(modelId: string): Promise<ModelSelfTest>
  GetModelDiagnostics(modelId: string): Promise<ModelDiagnostics>
  GetAccelStatus(): Promise<AccelStatus>
  /** "auto" | "cpu" */
  SetAccelMode(mode: string): Promise<AccelStatus>
  /** 下载并安装 GPU 运行时组件（进度走 chronos:accel / 轮询 GetAccelStatus）。 */
  InstallAccelRuntime(): Promise<AccelStatus>
}

/**
 * Wails 绑定：优先真实绑定，缺失时回退到同名 mock 实现。
 * 注意 Go 绑定方法依赖 `this`（Wails 生成的对象），必须用 apply 保留 receiver。
 */
function bind<K extends keyof Api>(name: K): Api[K] {
  const app = getDesktopApp()
  const fn = app?.[name]
  if (typeof fn === 'function') {
    return ((...args: unknown[]) => fn.apply(app, args)) as unknown as Api[K]
  }
  return mockApi[name]
}

/** 桌面（Wails）transport 的 Api 实现。 */
export function createWailsApi(): Api {
  return {
    GetAppInfo: bind('GetAppInfo'),
    ListModels: bind('ListModels'),
    RefreshModels: bind('RefreshModels'),
    UpscaleBatch: bind('UpscaleBatch'),
    CancelJob: bind('CancelJob'),
    SetConcurrency: bind('SetConcurrency'),
    GetConcurrency: bind('GetConcurrency'),
    SetInhibit: bind('SetInhibit'),
    GetInhibitStatus: bind('GetInhibitStatus'),
    PickImages: bind('PickImages'),
    PickOutputDir: bind('PickOutputDir'),
    Reveal: bind('Reveal'),
    OpenDataDir: bind('OpenDataDir'),
    ListDirectory: bind('ListDirectory'),
    PickModelFile: bind('PickModelFile'),
    PullModel: bind('PullModel'),
    CancelPull: bind('CancelPull'),
    RemoveModel: bind('RemoveModel'),
    ImportModel: bind('ImportModel'),
    SelfTestModel: bind('SelfTestModel'),
    GetModelDiagnostics: bind('GetModelDiagnostics'),
    GetAccelStatus: bind('GetAccelStatus'),
    SetAccelMode: bind('SetAccelMode'),
    InstallAccelRuntime: bind('InstallAccelRuntime'),
  }
}

/** 按 transport 构造 Api（导出便于单测注入 fetch / EventSource）。 */
export function createApi(transport: Transport, options?: HttpTransportOptions): Api {
  if (transport === 'http') return createHttpApi(options)
  if (transport === 'wails') return createWailsApi()
  return mockApi
}

export const api: Api = createApi(detectTransport())

/** 当前生效的 transport（每次动态探测）。 */
export function getTransport(): Transport {
  return detectTransport()
}

/** 浏览器模式：由 Go 本地 HTTP 服务提供页面。 */
export function isBrowserMode(): boolean {
  return detectTransport() === 'http'
}

/** 纯浏览器预览（vite dev，无后端）模式。 */
export function isMockMode(): boolean {
  return detectTransport() === 'mock'
}

/* ---------------------------------------------------------------- 事件订阅 */

let httpBus: HttpEventBus | null = null

function getHttpBus(): HttpEventBus {
  if (!httpBus) httpBus = createHttpEventBus()
  return httpBus
}

export function onEvent(name: 'chronos:progress', handler: (payload: ProgressEvent) => void): () => void
export function onEvent(name: 'chronos:job', handler: (payload: JobSummary) => void): () => void
export function onEvent(name: 'chronos:log', handler: (payload: LogEvent) => void): () => void
export function onEvent(name: 'chronos:inhibit', handler: (payload: InhibitStatus) => void): () => void
export function onEvent<T = unknown>(name: string, handler: (payload: T) => void): () => void
export function onEvent<T = unknown>(name: string, handler: (payload: T) => void): () => void {
  const deliver = (raw: unknown) => {
    try {
      handler(normalizeEvent(name, raw) as T)
    } catch {
      // 单个 handler 抛错不能中断事件循环
    }
  }

  const transport = detectTransport()
  if (transport === 'http') return getHttpBus().subscribe(name, deliver)
  if (transport === 'wails') {
    const runtime = getWailsRuntime()
    if (typeof runtime?.EventsOn === 'function') {
      const off = runtime.EventsOn(name, deliver)
      if (typeof off === 'function') return off
      // 某些 Wails 版本 EventsOn 返回 void，退化为 EventsOff
      return () => runtime.EventsOff?.(name)
    }
  }
  // 浏览器预览：走本地事件总线（mock 后端会 emit 同名字段结构的事件）
  return mockSubscribe(name, deliver)
}
