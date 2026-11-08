/**
 * 运行环境探测：Wails 注入的 `window.go` / `window.runtime`。
 *
 * 单独成文件是为了避免 `api.ts -> mock.ts -> media.ts -> api.ts` 的循环依赖；
 * `api.ts` 会再次导出 `isDesktop`，契约要求的调用点保持不变。
 */

export interface WailsAppBindings {
  GetAppInfo?: (...args: unknown[]) => Promise<unknown>
  ListModels?: (...args: unknown[]) => Promise<unknown>
  RefreshModels?: (...args: unknown[]) => Promise<unknown>
  UpscaleBatch?: (...args: unknown[]) => Promise<unknown>
  CancelJob?: (...args: unknown[]) => Promise<unknown>
  SetConcurrency?: (...args: unknown[]) => Promise<unknown>
  GetConcurrency?: (...args: unknown[]) => Promise<unknown>
  SetInhibit?: (...args: unknown[]) => Promise<unknown>
  GetInhibitStatus?: (...args: unknown[]) => Promise<unknown>
  PickImages?: (...args: unknown[]) => Promise<unknown>
  PickOutputDir?: (...args: unknown[]) => Promise<unknown>
  Reveal?: (...args: unknown[]) => Promise<unknown>
  OpenDataDir?: (...args: unknown[]) => Promise<unknown>
  [key: string]: ((...args: unknown[]) => unknown) | undefined
}

export interface WailsRuntime {
  EventsOn?: (name: string, callback: (payload: unknown) => void) => (() => void) | void
  EventsOff?: (name: string) => void
  EventsEmit?: (name: string, ...data: unknown[]) => void
  [key: string]: unknown
}

declare global {
  interface Window {
    go?: { main?: { App?: WailsAppBindings } }
    runtime?: WailsRuntime
  }
}

/** 契约 §5：`!!(window as any).go?.main?.App`。 */
export function isDesktop(): boolean {
  if (typeof window === 'undefined') return false
  const app = window.go?.main?.App
  return !!app && typeof app === 'object'
}

/** Wails 注入的 App 绑定（浏览器预览时为 undefined）。 */
export function getDesktopApp(): WailsAppBindings | undefined {
  if (typeof window === 'undefined') return undefined
  const app = window.go?.main?.App
  return app && typeof app === 'object' ? app : undefined
}

/** Wails 运行时（EventsOn / EventsOff）。 */
export function getWailsRuntime(): WailsRuntime | undefined {
  if (typeof window === 'undefined') return undefined
  const runtime = window.runtime
  return runtime && typeof runtime === 'object' ? runtime : undefined
}
