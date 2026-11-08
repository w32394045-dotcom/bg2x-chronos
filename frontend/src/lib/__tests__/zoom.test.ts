import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  DEFAULT_ZOOM,
  ZOOM_MAX,
  ZOOM_MIN,
  ZOOM_STEP,
  ZOOM_STORAGE_KEY,
  activeZoom,
  applyZoom,
  clampZoom,
  decreaseZoom,
  getZoom,
  getZoomShortcutHint,
  increaseZoom,
  initZoom,
  installZoomShortcuts,
  isEditableTarget,
  matchZoomShortcut,
  normalizeZoom,
  readStoredZoom,
  resetZoom,
  setZoom,
  stepZoom,
  type ZoomStorage,
} from '../zoom'

function fakeStorage(initial: Record<string, string> = {}): {
  storage: ZoomStorage
  raw: Map<string, string>
} {
  const raw = new Map(Object.entries(initial))
  return {
    raw,
    storage: {
      getItem: (key) => raw.get(key) ?? null,
      setItem: (key, value) => {
        raw.set(key, value)
      },
    },
  }
}

function fakeRoot(): { root: HTMLElement; styles: Map<string, string>; attrs: Map<string, string> } {
  const styles = new Map<string, string>()
  const attrs = new Map<string, string>()
  const root = {
    style: {
      setProperty: (name: string, value: string) => {
        styles.set(name, value)
      },
    },
    setAttribute: (name: string, value: string) => {
      attrs.set(name, value)
    },
  } as unknown as HTMLElement
  return { root, styles, attrs }
}

beforeEach(() => {
  // 模块级状态在测试间共享：每个用例前重置为 100%
  setZoom(DEFAULT_ZOOM, { storage: null, root: null })
})

describe('缩放范围与步进', () => {
  it('范围是 0.8–1.5，步进 0.1，默认 1', () => {
    expect(ZOOM_MIN).toBe(0.8)
    expect(ZOOM_MAX).toBe(1.5)
    expect(ZOOM_STEP).toBe(0.1)
    expect(DEFAULT_ZOOM).toBe(1)
  })

  it('clampZoom 收拢到范围并量化到 0.1', () => {
    expect(clampZoom(0.5)).toBe(0.8)
    expect(clampZoom(2)).toBe(1.5)
    expect(clampZoom(1)).toBe(1)
    expect(clampZoom(1.14)).toBe(1.1)
    expect(clampZoom(1.16)).toBe(1.2)
    expect(clampZoom(Number.NaN)).toBe(1)
    expect(clampZoom(Number.POSITIVE_INFINITY)).toBe(1)
  })

  it('normalizeZoom 解析持久化字符串，脏数据回落 1', () => {
    expect(normalizeZoom('1.2')).toBe(1.2)
    expect(normalizeZoom('0.5')).toBe(0.8)
    expect(normalizeZoom('abc')).toBe(1)
    expect(normalizeZoom('')).toBe(1)
    expect(normalizeZoom(null)).toBe(1)
    expect(normalizeZoom(undefined)).toBe(1)
    expect(normalizeZoom(1.3)).toBe(1.3)
  })

  it('stepZoom 以 0.1 为步进并在边界处停住', () => {
    expect(stepZoom(1, 1)).toBe(1.1)
    expect(stepZoom(1, -1)).toBe(0.9)
    expect(stepZoom(1, 5)).toBe(1.5)
    expect(stepZoom(1, -5)).toBe(0.8)
    expect(stepZoom(1.5, 1)).toBe(1.5)
    expect(stepZoom(0.8, -1)).toBe(0.8)
    // 浮点累加不产生 1.2000000000000002 之类的脏值
    expect(stepZoom(1.1, 1)).toBe(1.2)
  })
})

describe('缩放持久化与应用', () => {
  it('applyZoom 把 CSS zoom 写到根节点（#app 注入 fake）', () => {
    const { root, styles, attrs } = fakeRoot()
    expect(applyZoom(1.23, root)).toBe(1.2)
    expect(styles.get('zoom')).toBe('1.2')
    expect(attrs.get('data-zoom')).toBe('1.2')
    expect(activeZoom.value).toBe(1.2)

    expect(applyZoom(3, root)).toBe(1.5)
    expect(styles.get('zoom')).toBe('1.5')
  })

  it('setZoom 同时持久化并应用', () => {
    const { storage, raw } = fakeStorage()
    const { root, styles } = fakeRoot()

    expect(setZoom(1.3, { storage, root })).toBe(1.3)
    expect(raw.get(ZOOM_STORAGE_KEY)).toBe('1.3')
    expect(styles.get('zoom')).toBe('1.3')
    expect(getZoom()).toBe(1.3)
  })

  it('increaseZoom / decreaseZoom / resetZoom 按步进变化', () => {
    const { storage, raw } = fakeStorage()
    const { root } = fakeRoot()
    const deps = { storage, root }

    setZoom(1, deps)
    expect(increaseZoom(deps)).toBe(1.1)
    expect(raw.get(ZOOM_STORAGE_KEY)).toBe('1.1')
    expect(decreaseZoom(deps)).toBe(1)
    expect(decreaseZoom(deps)).toBe(0.9)
    expect(resetZoom(deps)).toBe(1)
    expect(raw.get(ZOOM_STORAGE_KEY)).toBe('1')
  })

  it('readStoredZoom / initZoom 从 localStorage 恢复', () => {
    const { storage } = fakeStorage({ [ZOOM_STORAGE_KEY]: '0.9' })
    expect(readStoredZoom(storage)).toBe(0.9)

    const { root, styles } = fakeRoot()
    expect(initZoom({ storage, root })).toBe(0.9)
    expect(styles.get('zoom')).toBe('0.9')
    expect(getZoom()).toBe(0.9)
  })

  it('initZoom 无存储时应用 100%', () => {
    const { root, styles } = fakeRoot()
    expect(initZoom({ storage: null, root })).toBe(1)
    expect(styles.get('zoom')).toBe('1')
  })

  it('存储写入抛错时缩放仍然生效', () => {
    const storage: ZoomStorage = {
      getItem: () => null,
      setItem: () => {
        throw new Error('QuotaExceededError')
      },
    }
    const { root, styles } = fakeRoot()
    expect(setZoom(1.4, { storage, root })).toBe(1.4)
    expect(styles.get('zoom')).toBe('1.4')
  })
})

describe('快捷键映射', () => {
  it('桌面（wails）：Ctrl + = / - / 0', () => {
    expect(matchZoomShortcut({ key: '=', ctrlKey: true }, 'wails')).toBe('in')
    expect(matchZoomShortcut({ key: '+', ctrlKey: true }, 'wails')).toBe('in')
    expect(matchZoomShortcut({ key: '-', ctrlKey: true }, 'wails')).toBe('out')
    expect(matchZoomShortcut({ key: '0', ctrlKey: true }, 'wails')).toBe('reset')
    // macOS：Cmd 视同 Ctrl
    expect(matchZoomShortcut({ key: '=', metaKey: true }, 'wails')).toBe('in')
  })

  it('桌面不接管带 Shift 的组合', () => {
    expect(matchZoomShortcut({ key: '=', ctrlKey: true, shiftKey: true }, 'wails')).toBeNull()
    expect(matchZoomShortcut({ key: '-', ctrlKey: true, shiftKey: true }, 'wails')).toBeNull()
    expect(matchZoomShortcut({ key: '0', ctrlKey: true, shiftKey: true }, 'wails')).toBeNull()
  })

  it('浏览器（http）：Ctrl + Shift + = / - / 0', () => {
    expect(matchZoomShortcut({ key: '=', ctrlKey: true, shiftKey: true }, 'http')).toBe('in')
    expect(matchZoomShortcut({ key: '+', ctrlKey: true, shiftKey: true }, 'http')).toBe('in')
    expect(matchZoomShortcut({ key: '-', ctrlKey: true, shiftKey: true }, 'http')).toBe('out')
    expect(matchZoomShortcut({ key: '_', ctrlKey: true, shiftKey: true }, 'http')).toBe('out')
    expect(matchZoomShortcut({ key: '0', ctrlKey: true, shiftKey: true }, 'http')).toBe('reset')
    // US 布局下 Shift+0 的 event.key 是 ')'
    expect(matchZoomShortcut({ key: ')', ctrlKey: true, shiftKey: true }, 'http')).toBe('reset')
  })

  it('浏览器不接管不带 Shift 的组合（留给浏览器自身缩放）', () => {
    expect(matchZoomShortcut({ key: '=', ctrlKey: true }, 'http')).toBeNull()
    expect(matchZoomShortcut({ key: '-', ctrlKey: true }, 'http')).toBeNull()
    expect(matchZoomShortcut({ key: '0', ctrlKey: true }, 'http')).toBeNull()
    // mock（vite dev 预览）按浏览器处理
    expect(matchZoomShortcut({ key: '=', ctrlKey: true, shiftKey: true }, 'mock')).toBe('in')
    expect(matchZoomShortcut({ key: '=', ctrlKey: true }, 'mock')).toBeNull()
  })

  it('无 Ctrl/Cmd、带 Alt、或无关按键都返回 null', () => {
    expect(matchZoomShortcut({ key: '=' }, 'wails')).toBeNull()
    expect(matchZoomShortcut({ key: '=', ctrlKey: true, altKey: true }, 'wails')).toBeNull()
    expect(matchZoomShortcut({ key: 'z', ctrlKey: true, shiftKey: true }, 'http')).toBeNull()
    expect(matchZoomShortcut({ key: 'Enter', ctrlKey: true }, 'wails')).toBeNull()
  })

  it('event.key 不稳定时用 event.code 兜底', () => {
    expect(matchZoomShortcut({ key: 'Unidentified', ctrlKey: true, code: 'Equal' }, 'wails')).toBe('in')
    expect(matchZoomShortcut({ key: 'Unidentified', ctrlKey: true, code: 'Minus' }, 'wails')).toBe('out')
    expect(
      matchZoomShortcut({ key: 'Unidentified', ctrlKey: true, shiftKey: true, code: 'Digit0' }, 'http'),
    ).toBe('reset')
  })

  it('提示文案区分桌面 / 浏览器', () => {
    expect(getZoomShortcutHint('wails')).toContain('Ctrl + =')
    expect(getZoomShortcutHint('wails')).not.toContain('Shift')
    expect(getZoomShortcutHint('http')).toContain('Ctrl + Shift + =')
    expect(getZoomShortcutHint('mock')).toContain('Ctrl + Shift')
  })
})

describe('输入框焦点判定', () => {
  it('input / textarea / select / contenteditable 内不响应', () => {
    expect(isEditableTarget({ tagName: 'INPUT' })).toBe(true)
    expect(isEditableTarget({ tagName: 'TEXTAREA' })).toBe(true)
    expect(isEditableTarget({ tagName: 'SELECT' })).toBe(true)
    expect(isEditableTarget({ tagName: 'DIV', isContentEditable: true })).toBe(true)
  })

  it('普通元素 / 空值不拦截', () => {
    expect(isEditableTarget({ tagName: 'DIV' })).toBe(false)
    expect(isEditableTarget({ tagName: 'BUTTON' })).toBe(false)
    expect(isEditableTarget(null)).toBe(false)
    expect(isEditableTarget(undefined)).toBe(false)
    expect(isEditableTarget('input')).toBe(false)
  })
})

describe('installZoomShortcuts', () => {
  function makeTarget(): {
    target: Window
    dispatch: (event: Record<string, unknown>) => void
    listenerCount: () => number
  } {
    let listener: ((event: KeyboardEvent) => void) | null = null
    const target = {
      addEventListener: (type: string, fn: (event: KeyboardEvent) => void) => {
        if (type === 'keydown') listener = fn
      },
      removeEventListener: (type: string, fn: (event: KeyboardEvent) => void) => {
        if (type === 'keydown' && listener === fn) listener = null
      },
    } as unknown as Window
    return {
      target,
      dispatch: (event) => {
        listener?.(event as unknown as KeyboardEvent)
      },
      listenerCount: () => (listener ? 1 : 0),
    }
  }

  it('命中快捷键时 preventDefault 并缩放；卸载后不再响应', () => {
    const { target, dispatch, listenerCount } = makeTarget()
    const { root, styles } = fakeRoot()
    const off = installZoomShortcuts({ transport: 'wails', target, root })
    expect(listenerCount()).toBe(1)

    const handled = { key: '=', ctrlKey: true, target: { tagName: 'DIV' }, preventDefault: vi.fn() }
    dispatch(handled)
    expect(handled.preventDefault).toHaveBeenCalledTimes(1)
    expect(styles.get('zoom')).toBe('1.1')

    off()
    expect(listenerCount()).toBe(0)
  })

  it('不匹配的按键 / 输入框焦点 / 已 preventDefault 时不拦截', () => {
    const { target, dispatch } = makeTarget()
    const { root, styles } = fakeRoot()
    installZoomShortcuts({ transport: 'wails', target, root })

    const unhandled = { key: '=', ctrlKey: true, shiftKey: true, target: { tagName: 'DIV' }, preventDefault: vi.fn() }
    dispatch(unhandled)
    expect(unhandled.preventDefault).not.toHaveBeenCalled()

    const typing = { key: '=', ctrlKey: true, target: { tagName: 'INPUT' }, preventDefault: vi.fn() }
    dispatch(typing)
    expect(typing.preventDefault).not.toHaveBeenCalled()

    const already = { key: '=', ctrlKey: true, target: { tagName: 'DIV' }, defaultPrevented: true, preventDefault: vi.fn() }
    dispatch(already)
    expect(already.preventDefault).not.toHaveBeenCalled()
    // 以上都没有真正缩放
    expect(styles.has('zoom')).toBe(false)
  })

  it('浏览器模式下 Ctrl+Shift+- 触发缩小', () => {
    const { target, dispatch } = makeTarget()
    const { root, styles } = fakeRoot()
    installZoomShortcuts({ transport: 'http', target, root })
    setZoom(1.2, { storage: null, root: null })

    const handled = { key: '-', ctrlKey: true, shiftKey: true, target: { tagName: 'DIV' }, preventDefault: vi.fn() }
    dispatch(handled)
    expect(handled.preventDefault).toHaveBeenCalledTimes(1)
    expect(styles.get('zoom')).toBe('1.1')
  })
})
