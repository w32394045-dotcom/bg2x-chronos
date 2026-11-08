/**
 * HTTP 传输层（浏览器模式，用于没有 WebView2 / WebKit 的机器）。
 *
 * Go 侧 `chronos serve` 在 index.html 注入 `window.__CHRONOS_HTTP__ = true`，
 * 并暴露两个端点：
 *   POST /api/<MethodName>  body {"args":[...]}  -> {"result": ...} | {"error": "..."}
 *   GET  /events            SSE  data: {"name":"chronos:progress","data":{...}}
 *
 * 本文件只负责协议编解码与连接管理，不参与业务状态；
 * `api.ts` 负责按运行环境选择 transport，`useChronos.ts` 消费归一化后的事件。
 */
import { isDesktop, isHttpMode } from './env'
import { CHRONOS_EVENTS } from './events'
import type { Api } from './api'

export type Transport = 'wails' | 'http' | 'mock'

/**
 * 传输选择（优先级：HTTP 标记 > Wails 绑定 > mock）。
 * 纯函数：每次调用都重新探测，便于浏览器模式在 Wails 旧页面上热切换。
 */
export function detectTransport(): Transport {
  if (isHttpMode()) return 'http'
  if (isDesktop()) return 'wails'
  return 'mock'
}

export const RPC_PREFIX = '/api'
export const EVENTS_PATH = '/events'

/** 去掉尾部斜杠；空/未定义保持相对路径（同源）。 */
export function normalizeBase(base?: string): string {
  if (!base) return ''
  return base.trim().replace(/\/+$/, '')
}

/** POST /api/<Method>（可带 base，便于反向代理前缀或单测）。 */
export function buildRpcUrl(method: string, base?: string): string {
  return `${normalizeBase(base)}${RPC_PREFIX}/${method}`
}

/** SSE 端点 URL。 */
export function buildEventsUrl(base?: string): string {
  return `${normalizeBase(base)}${EVENTS_PATH}`
}

/* ------------------------------------------------------------------ RPC 编解码 */

export interface FetchResponseLike {
  ok: boolean
  status: number
  json(): Promise<unknown>
}

export type FetchLike = (input: string, init?: RequestInit) => Promise<FetchResponseLike>

/**
 * 解析 {result} / {error} 信封。
 * - error 非空 => 抛 Error（Go 的 error 走这里）
 * - 有 result 字段 => 返回（可能是 null）
 * - 其余视为协议错误
 */
export function parseRpcPayload(payload: unknown): unknown {
  if (!payload || typeof payload !== 'object') {
    throw new Error('RPC 响应不是 JSON 对象')
  }
  const record = payload as Record<string, unknown>
  const error = record.error
  if (typeof error === 'string' && error.trim() !== '') throw new Error(error)
  if ('result' in record) return record.result
  throw new Error('RPC 响应缺少 result 字段')
}

/** 从响应里尽量取出人话错误信息。 */
async function readErrorMessage(response: FetchResponseLike, method: string): Promise<string> {
  try {
    const payload = await response.json()
    if (payload && typeof payload === 'object') {
      const error = (payload as Record<string, unknown>).error
      if (typeof error === 'string' && error.trim() !== '') return error
    }
  } catch {
    // 非 JSON 响应体：忽略
  }
  return `请求 ${method} 失败（HTTP ${response.status}）`
}

/* ------------------------------------------------------------------- SSE 解码 */

export interface SseMessage {
  name: string
  data: unknown
}

/**
 * SSE 单条消息解码。
 *
 * 标准形态：`data: {"name":"chronos:progress","data":{...}}`
 * 兼容形态：Go 若用 `event: chronos:progress` + 裸 payload 发送，则通过 fallbackName 补名字。
 * 非法 JSON / 非对象 / 未知形态返回 null（调用方直接丢弃，绝不让单条坏消息中断事件流）。
 */
export function parseSseMessage(raw: unknown, fallbackName?: string): SseMessage | null {
  let payload: unknown = raw
  if (typeof raw === 'string') {
    const text = raw.trim()
    if (!text) return null
    try {
      payload = JSON.parse(text)
    } catch {
      return null
    }
  }
  if (!payload || typeof payload !== 'object') return null
  const record = payload as Record<string, unknown>
  const rawName = record.name
  const name = typeof rawName === 'string' && rawName !== '' ? rawName : fallbackName
  if (!name) return null
  const hasDataField = 'data' in record
  return { name, data: hasDataField ? record.data : record }
}

/* --------------------------------------------------------------- HTTP Api 实现 */

export interface EventSourceLike {
  onmessage: ((event: { data: unknown }) => void) | null
  onerror?: ((event: unknown) => void) | null
  addEventListener?: (name: string, handler: (event: { data: unknown }) => void) => void
  close: () => void
}

export interface HttpTransportOptions {
  /** 同源部署时为 ''；反向代理或单测可传绝对地址 */
  base?: string
  fetchImpl?: FetchLike
  eventSourceFactory?: (url: string) => EventSourceLike | null
}

function defaultEventSourceFactory(url: string): EventSourceLike | null {
  if (typeof EventSource === 'undefined') return null
  const source = new EventSource(url)
  return source as unknown as EventSourceLike
}

/** 构造基于 HTTP 的 Api（方法与 §5 一一对应）。 */
export function createHttpApi(options: HttpTransportOptions = {}): Api {
  const fetchImpl: FetchLike =
    options.fetchImpl ?? ((input, init) => fetch(input, init) as unknown as Promise<FetchResponseLike>)

  async function rpc<T>(method: string, args: unknown[]): Promise<T> {
    let response: FetchResponseLike
    try {
      response = await fetchImpl(buildRpcUrl(method, options.base), {
        method: 'POST',
        headers: { 'content-type': 'application/json', accept: 'application/json' },
        body: JSON.stringify({ args }),
        credentials: 'same-origin',
      })
    } catch (error) {
      const detail = error instanceof Error ? error.message : String(error)
      throw new Error(`无法连接本地服务：${detail}`)
    }
    if (!response.ok) throw new Error(await readErrorMessage(response, method))
    let payload: unknown
    try {
      payload = await response.json()
    } catch {
      throw new Error(`请求 ${method} 返回了非 JSON 响应`)
    }
    return parseRpcPayload(payload) as T
  }

  return {
    GetAppInfo: () => rpc('GetAppInfo', []),
    ListModels: () => rpc('ListModels', []),
    RefreshModels: () => rpc('RefreshModels', []),
    UpscaleBatch: (req) => rpc('UpscaleBatch', [req]),
    CancelJob: (jobId) => rpc('CancelJob', [jobId]),
    SetConcurrency: (n) => rpc('SetConcurrency', [n]),
    GetConcurrency: () => rpc('GetConcurrency', []),
    SetInhibit: (req) => rpc('SetInhibit', [req]),
    GetInhibitStatus: () => rpc('GetInhibitStatus', []),
    PickImages: () => rpc('PickImages', []),
    PickOutputDir: () => rpc('PickOutputDir', []),
    Reveal: (path) => rpc('Reveal', [path]),
    OpenDataDir: () => rpc('OpenDataDir', []),
    ListDirectory: (path) => rpc('ListDirectory', [path]),
  }
}

/* -------------------------------------------------------------- SSE 事件总线 */

export interface HttpEventBus {
  subscribe(name: string, handler: (data: unknown) => void): () => void
  close(): void
  isConnected(): boolean
}

const KNOWN_EVENT_NAMES: string[] = Object.values(CHRONOS_EVENTS)

/**
 * 单条 EventSource 连接 + 按事件名分发。
 * EventSource 自带断线重连；最后一个订阅者退订时关闭连接。
 */
export function createHttpEventBus(options: HttpTransportOptions = {}): HttpEventBus {
  const listeners = new Map<string, Set<(data: unknown) => void>>()
  let source: EventSourceLike | null = null

  function dispatch(message: SseMessage): void {
    const set = listeners.get(message.name)
    if (!set) return
    for (const listener of [...set]) {
      try {
        listener(message.data)
      } catch {
        // 单个订阅者异常不能影响其它订阅者
      }
    }
  }

  function handle(raw: unknown, fallbackName?: string): void {
    const message = parseSseMessage(raw, fallbackName)
    if (message) dispatch(message)
  }

  function ensureSource(): EventSourceLike | null {
    if (source) return source
    const factory = options.eventSourceFactory ?? defaultEventSourceFactory
    try {
      source = factory(buildEventsUrl(options.base))
    } catch {
      // EventSource 不可用（极旧浏览器 / 测试环境）：降级为无事件，UI 仍可操作
      source = null
    }
    if (!source) return null
    source.onmessage = (event) => handle(event.data)
    for (const name of KNOWN_EVENT_NAMES) {
      // 兼容 Go 用 `event: chronos:progress` 命名事件发送（此时不触发 onmessage）
      source.addEventListener?.(name, (event) => handle(event.data, name))
    }
    return source
  }

  function close(): void {
    source?.close()
    source = null
  }

  return {
    subscribe(name, handler) {
      let set = listeners.get(name)
      if (!set) {
        set = new Set()
        listeners.set(name, set)
      }
      set.add(handler)
      ensureSource()
      return () => {
        const current = listeners.get(name)
        if (!current) return
        current.delete(handler)
        if (current.size === 0) listeners.delete(name)
        if (listeners.size === 0) close()
      }
    },
    close,
    isConnected() {
      return source !== null
    },
  }
}
