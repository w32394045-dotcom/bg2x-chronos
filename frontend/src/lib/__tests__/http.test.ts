import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  buildEventsUrl,
  buildRpcUrl,
  createHttpApi,
  createHttpEventBus,
  detectTransport,
  normalizeBase,
  parseRpcPayload,
  parseSseMessage,
  type EventSourceLike,
  type FetchLike,
} from '../http'

describe('buildRpcUrl / buildEventsUrl', () => {
  it('同源部署使用相对路径', () => {
    expect(buildRpcUrl('GetAppInfo')).toBe('/api/GetAppInfo')
    expect(buildEventsUrl()).toBe('/events')
  })

  it('可带 base（自动去掉尾斜杠）', () => {
    expect(normalizeBase('http://127.0.0.1:5000/')).toBe('http://127.0.0.1:5000')
    expect(normalizeBase(undefined)).toBe('')
    expect(buildRpcUrl('ListDirectory', 'http://127.0.0.1:5000/')).toBe(
      'http://127.0.0.1:5000/api/ListDirectory',
    )
    expect(buildEventsUrl('/chronos/')).toBe('/chronos/events')
  })

  it('方法名原样拼接（不做 URL 编码变形）', () => {
    expect(buildRpcUrl('UpscaleBatch')).toBe('/api/UpscaleBatch')
    expect(buildRpcUrl('SetInhibit', '')).toBe('/api/SetInhibit')
  })
})

describe('parseRpcPayload', () => {
  it('返回 result', () => {
    expect(parseRpcPayload({ result: { queued: 2 } })).toEqual({ queued: 2 })
  })

  it('result 为 null 是合法响应（void 方法）', () => {
    expect(parseRpcPayload({ result: null })).toBeNull()
  })

  it('error 非空时抛出原始 Go 错误', () => {
    expect(() => parseRpcPayload({ error: '并发数必须在 1~8 之间' })).toThrow('1~8')
  })

  it('非法信封抛出协议错误', () => {
    expect(() => parseRpcPayload(null)).toThrow()
    expect(() => parseRpcPayload('{}')).toThrow()
    expect(() => parseRpcPayload({})).toThrow(/result/)
  })
})

describe('parseSseMessage', () => {
  it('解析标准 {name,data} 消息', () => {
    expect(parseSseMessage('{"name":"chronos:progress","data":{"file":"a.jpg","done":3}}')).toEqual({
      name: 'chronos:progress',
      data: { file: 'a.jpg', done: 3 },
    })
  })

  it('命名事件（裸 payload）用 fallbackName 补名字', () => {
    expect(parseSseMessage('{"level":"info","msg":"hi"}', 'chronos:log')).toEqual({
      name: 'chronos:log',
      data: { level: 'info', msg: 'hi' },
    })
  })

  it('data 为 null 时原样保留', () => {
    expect(parseSseMessage('{"name":"chronos:job","data":null}')).toEqual({
      name: 'chronos:job',
      data: null,
    })
  })

  it('坏 JSON / 空串 / 非对象 / 缺 name 返回 null', () => {
    expect(parseSseMessage('not json')).toBeNull()
    expect(parseSseMessage('')).toBeNull()
    expect(parseSseMessage('null')).toBeNull()
    expect(parseSseMessage('{"foo":1}')).toBeNull()
  })
})

function recorder(
  body: unknown,
  status = 200,
): { calls: { url: string; init?: RequestInit }[]; fetchImpl: FetchLike } {
  const calls: { url: string; init?: RequestInit }[] = []
  const fetchImpl: FetchLike = async (url, init) => {
    calls.push({ url, init })
    return { ok: status >= 200 && status < 300, status, json: async () => body }
  }
  return { calls, fetchImpl }
}

describe('createHttpApi', () => {
  it('POST /api/<Method>，body 为 {"args":[...]}', async () => {
    const { calls, fetchImpl } = recorder({ result: { version: '0.2.0' } })
    const httpApi = createHttpApi({ fetchImpl, base: 'http://127.0.0.1:3210/' })
    const info = await httpApi.GetAppInfo()
    expect(info.version).toBe('0.2.0')
    expect(calls[0].url).toBe('http://127.0.0.1:3210/api/GetAppInfo')
    expect(calls[0].init?.method).toBe('POST')
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({ args: [] })
  })

  it('ListDirectory 透传路径参数', async () => {
    const { calls, fetchImpl } = recorder({
      result: { path: 'C:\\x', parent: 'C:\\', entries: [] },
    })
    const listing = await createHttpApi({ fetchImpl }).ListDirectory('C:\\x')
    expect(listing.path).toBe('C:\\x')
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({ args: ['C:\\x'] })
  })

  it('error 信封 / 非 2xx 都 reject', async () => {
    await expect(
      createHttpApi({ fetchImpl: recorder({ error: '并发数必须在 1~8 之间' }).fetchImpl }).SetConcurrency(9),
    ).rejects.toThrow(/1~8/)
    await expect(
      createHttpApi({ fetchImpl: recorder({ error: 'bad request' }, 400).fetchImpl }).CancelJob('x'),
    ).rejects.toThrow(/bad request/)
  })

  it('网络失败给出可读错误', async () => {
    const failing: FetchLike = async () => {
      throw new Error('ECONNREFUSED')
    }
    await expect(createHttpApi({ fetchImpl: failing }).GetAppInfo()).rejects.toThrow(/无法连接本地服务/)
  })
})

function createFakeEventSource(): {
  factory: (url: string) => EventSourceLike
  get: () => {
    url: string
    onmessage: ((event: { data: unknown }) => void) | null
    listeners: Record<string, (event: { data: unknown }) => void>
    closed: boolean
  } | null
} {
  let instance: {
    url: string
    onmessage: ((event: { data: unknown }) => void) | null
    listeners: Record<string, (event: { data: unknown }) => void>
    closed: boolean
  } | null = null

  function factory(url: string): EventSourceLike {
    const created = {
      url,
      onmessage: null as ((event: { data: unknown }) => void) | null,
      listeners: {} as Record<string, (event: { data: unknown }) => void>,
      closed: false,
      addEventListener(name: string, handler: (event: { data: unknown }) => void) {
        created.listeners[name] = handler
      },
      close() {
        created.closed = true
      },
    }
    instance = created
    return created as unknown as EventSourceLike
  }

  return { factory, get: () => instance }
}

describe('createHttpEventBus', () => {
  it('连接 /events，按 name 分发 data，最后退订后关闭', () => {
    const fake = createFakeEventSource()
    const bus = createHttpEventBus({ eventSourceFactory: fake.factory })
    const progress: unknown[] = []
    const off = bus.subscribe('chronos:progress', (data) => progress.push(data))

    const source = fake.get()
    expect(source?.url).toBe('/events')
    expect(bus.isConnected()).toBe(true)

    source?.onmessage?.({ data: JSON.stringify({ name: 'chronos:progress', data: { done: 4 } }) })
    expect(progress).toEqual([{ done: 4 }])

    // 未订阅的事件不会误分发
    source?.onmessage?.({ data: JSON.stringify({ name: 'chronos:other', data: 1 }) })
    expect(progress).toHaveLength(1)
    // 坏消息被丢弃，不抛错
    source?.onmessage?.({ data: '{oops' })
    expect(progress).toHaveLength(1)

    off()
    expect(source?.closed).toBe(true)
  })

  it('命名事件 event: chronos:log 也能分发（裸 payload）', () => {
    const fake = createFakeEventSource()
    const bus = createHttpEventBus({ eventSourceFactory: fake.factory })
    const logs: unknown[] = []
    const off = bus.subscribe('chronos:log', (data) => logs.push(data))

    fake.get()?.listeners['chronos:log']?.({ data: JSON.stringify({ level: 'warn', msg: 'w' }) })
    expect(logs).toEqual([{ level: 'warn', msg: 'w' }])
    off()
  })

  it('多个订阅者共享同一条 EventSource 连接', () => {
    const fake = createFakeEventSource()
    let connections = 0
    const bus = createHttpEventBus({
      eventSourceFactory: (url) => {
        connections += 1
        return fake.factory(url)
      },
    })
    const a: unknown[] = []
    const b: unknown[] = []
    const offA = bus.subscribe('chronos:progress', (data) => a.push(data))
    const offB = bus.subscribe('chronos:job', (data) => b.push(data))
    expect(connections).toBe(1)

    const source = fake.get()
    source?.onmessage?.({ data: JSON.stringify({ name: 'chronos:progress', data: 1 }) })
    offA()
    expect(source?.closed).toBe(false)
    offB()
    expect(source?.closed).toBe(true)
    expect(a).toEqual([1])
  })
})

describe('detectTransport', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('无后端时回退 mock', () => {
    vi.stubGlobal('window', {})
    expect(detectTransport()).toBe('mock')
  })

  it('window.__CHRONOS_HTTP__ === true => http', () => {
    vi.stubGlobal('window', { __CHRONOS_HTTP__: true })
    expect(detectTransport()).toBe('http')
  })

  it('HTTP 标记优先于 Wails 绑定（兼容旧页面残留 window.go）', () => {
    vi.stubGlobal('window', { __CHRONOS_HTTP__: true, go: { main: { App: {} } } })
    expect(detectTransport()).toBe('http')
  })

  it('仅 Wails 绑定时为 wails', () => {
    vi.stubGlobal('window', { go: { main: { App: {} } } })
    expect(detectTransport()).toBe('wails')
  })

  it('标记非布尔 true 不算浏览器模式', () => {
    vi.stubGlobal('window', { __CHRONOS_HTTP__: 'yes' })
    expect(detectTransport()).toBe('mock')
  })
})
