/**
 * 前端唯一的后端入口（契约 §5）。
 *
 * 桌面（Wails）下每个方法都懒绑定到 `window.go.main.App.<Method>`；
 * 浏览器预览（`vite dev`）下自动降级到 mock，逐方法回退，
 * 因此即使某个绑定缺失（旧的 Wails 二进制）UI 也不会整体崩溃。
 */
import { getDesktopApp, getWailsRuntime, isDesktop } from './env'
import { normalizeEvent } from './events'
import { mockApi } from './mock'
import { mockSubscribe } from './eventbus'
import type {
  AppInfo,
  BatchRequest,
  BatchResult,
  InhibitRequest,
  InhibitStatus,
  JobSummary,
  LogEvent,
  ModelInfo,
  ProgressEvent,
} from './types'

export { isDesktop }

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

/**
 * 方法绑定：优先真实 Wails 绑定，缺失时回退到同名 mock 实现。
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

export const api: Api = {
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
}

/** 当前是否运行在浏览器预览（mock）模式。 */
export function isMockMode(): boolean {
  const app = getDesktopApp()
  return !isDesktop() || typeof app?.GetAppInfo !== 'function'
}

/* ---------------------------------------------------------------- 事件订阅 */

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
  const runtime = getWailsRuntime()
  if (typeof runtime?.EventsOn === 'function') {
    const off = runtime.EventsOn(name, deliver)
    if (typeof off === 'function') return off
    // 某些 Wails 版本 EventsOn 返回 void，退化为 EventsOff
    return () => runtime.EventsOff?.(name)
  }
  // 浏览器预览：走本地事件总线（mock 后端会 emit 同名字段结构的事件）
  return mockSubscribe(name, deliver)
}
