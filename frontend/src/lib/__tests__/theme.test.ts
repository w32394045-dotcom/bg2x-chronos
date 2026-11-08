import { beforeEach, describe, expect, it } from 'vitest'
import {
  DEFAULT_THEME,
  THEME_STORAGE_KEY,
  THEMES,
  activeTheme,
  applyTheme,
  getTheme,
  initTheme,
  isTheme,
  nextTheme,
  readStoredTheme,
  setTheme,
  themeColorScheme,
  type ThemeStorage,
} from '../theme'

function fakeStorage(initial: Record<string, string> = {}): {
  storage: ThemeStorage
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

function fakeRoot(): {
  root: HTMLElement
  attrs: Map<string, string>
  styles: Map<string, string>
} {
  const attrs = new Map<string, string>()
  const styles = new Map<string, string>()
  const root = {
    setAttribute: (name: string, value: string) => {
      attrs.set(name, value)
    },
    getAttribute: (name: string) => attrs.get(name) ?? null,
    style: {
      setProperty: (name: string, value: string) => {
        styles.set(name, value)
      },
    },
  } as unknown as HTMLElement
  return { root, attrs, styles }
}

beforeEach(() => {
  // 模块级状态在测试间共享：每个用例前重置为默认深色
  applyTheme('dark', null)
})

describe('主题常量与默认值', () => {
  it('默认主题是 dark，三套主题齐全且顺序稳定', () => {
    expect(DEFAULT_THEME).toBe('dark')
    expect(THEMES).toEqual(['dark', 'light', 'claude'])
    expect(new Set(THEMES).size).toBe(3)
  })

  it('无存储时读取到默认主题', () => {
    expect(readStoredTheme(null)).toBe('dark')
    expect(getTheme()).toBe('dark')
  })

  it('color-scheme：dark 为 dark，light / claude 为 light', () => {
    expect(themeColorScheme('dark')).toBe('dark')
    expect(themeColorScheme('light')).toBe('light')
    expect(themeColorScheme('claude')).toBe('light')
  })

  it('isTheme 只接受三套主题字符串', () => {
    expect(isTheme('dark')).toBe(true)
    expect(isTheme('light')).toBe(true)
    expect(isTheme('claude')).toBe(true)
    expect(isTheme('system')).toBe(false)
    expect(isTheme('')).toBe(false)
    expect(isTheme(null)).toBe(false)
    expect(isTheme(42)).toBe(false)
  })

  it('nextTheme 顺序循环：dark → light → claude → dark', () => {
    expect(nextTheme('dark')).toBe('light')
    expect(nextTheme('light')).toBe('claude')
    expect(nextTheme('claude')).toBe('dark')
  })
})

describe('主题持久化', () => {
  it('读取 localStorage 中已保存的主题', () => {
    expect(readStoredTheme(fakeStorage({ [THEME_STORAGE_KEY]: 'light' }).storage)).toBe('light')
    expect(readStoredTheme(fakeStorage({ [THEME_STORAGE_KEY]: 'claude' }).storage)).toBe('claude')
  })

  it('非法持久化值回落到 dark', () => {
    expect(readStoredTheme(fakeStorage({ [THEME_STORAGE_KEY]: 'solarized' }).storage)).toBe('dark')
    expect(readStoredTheme(fakeStorage({ [THEME_STORAGE_KEY]: '' }).storage)).toBe('dark')
  })

  it('setTheme 同时写存储、data-theme 与 color-scheme', () => {
    const { storage, raw } = fakeStorage()
    const { root, attrs, styles } = fakeRoot()

    expect(setTheme('claude', { storage, root })).toBe('claude')
    expect(raw.get(THEME_STORAGE_KEY)).toBe('claude')
    expect(attrs.get('data-theme')).toBe('claude')
    expect(styles.get('color-scheme')).toBe('light')
    expect(getTheme()).toBe('claude')
  })

  it('applyTheme 只应用不写存储；非法参数回落 dark', () => {
    const { raw } = fakeStorage()
    const { root, attrs, styles } = fakeRoot()

    expect(applyTheme('light', root)).toBe('light')
    expect(attrs.get('data-theme')).toBe('light')
    expect(styles.get('color-scheme')).toBe('light')
    expect(raw.size).toBe(0)
    expect(activeTheme.value).toBe('light')

    expect(applyTheme('nope' as never, root)).toBe('dark')
    expect(attrs.get('data-theme')).toBe('dark')
    expect(styles.get('color-scheme')).toBe('dark')
  })

  it('initTheme 从存储恢复主题并应用到 <html>', () => {
    const { storage } = fakeStorage({ [THEME_STORAGE_KEY]: 'light' })
    const { root, attrs, styles } = fakeRoot()

    expect(initTheme({ storage, root })).toBe('light')
    expect(attrs.get('data-theme')).toBe('light')
    expect(styles.get('color-scheme')).toBe('light')
    expect(activeTheme.value).toBe('light')
  })

  it('initTheme 在无存储时应用 dark', () => {
    const { root, attrs } = fakeRoot()
    expect(initTheme({ storage: null, root })).toBe('dark')
    expect(attrs.get('data-theme')).toBe('dark')
  })

  it('存储写入抛错时主题仍然生效', () => {
    const storage: ThemeStorage = {
      getItem: () => null,
      setItem: () => {
        throw new Error('QuotaExceededError')
      },
    }
    const { root, attrs } = fakeRoot()
    expect(setTheme('light', { storage, root })).toBe('light')
    expect(attrs.get('data-theme')).toBe('light')
  })
})
