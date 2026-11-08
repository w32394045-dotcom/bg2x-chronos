/**
 * 极简事件总线：浏览器预览时替代 Wails 的 window.runtime.EventsOn。
 * 与 mock.ts 拆开是为了避免 mock → api → mock 的运行时循环依赖。
 */

type Listener = (payload: unknown) => void

const listeners = new Map<string, Set<Listener>>()

export function mockSubscribe(name: string, listener: Listener): () => void {
  let set = listeners.get(name)
  if (!set) {
    set = new Set()
    listeners.set(name, set)
  }
  set.add(listener)
  return () => {
    const current = listeners.get(name)
    if (!current) return
    current.delete(listener)
    if (current.size === 0) listeners.delete(name)
  }
}

export function mockEmit(name: string, payload: unknown): void {
  const set = listeners.get(name)
  if (!set) return
  for (const listener of [...set]) {
    try {
      listener(payload)
    } catch {
      // 单个订阅者异常不能影响其他订阅者（与 Wails 运行时行为一致）
    }
  }
}

/** 清空所有订阅（测试用）。 */
export function mockResetBus(): void {
  listeners.clear()
}
