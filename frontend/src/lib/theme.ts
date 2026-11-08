/**
 * 主题（外观）状态：dark / light / claude。
 *
 * 颜色本身全部由 src/style.css 里的 CSS 变量定义，本模块只负责
 * 1) 读写 localStorage（key：`chronos.theme`，默认 dark）；
 * 2) 把主题写到 <html data-theme="..."> 并同步 color-scheme；
 * 3) 暴露一个只读响应式 ref 给 Vue 组件（HeaderBar / SettingsModal）联动。
 *
 * 所有 DOM / localStorage 访问都做了降级：在 node（vitest）或受限 WebView
 * 里不会抛错，只是不产生副作用；单测可通过 deps 注入 fake storage / root。
 */
import { readonly, ref } from 'vue'

export type Theme = 'dark' | 'light' | 'claude'

/** 三套主题，顺序即 UI 上的展示顺序。 */
export const THEMES: readonly Theme[] = ['dark', 'light', 'claude']

export const DEFAULT_THEME: Theme = 'dark'
export const THEME_STORAGE_KEY = 'chronos.theme'

export const THEME_LABEL: Record<Theme, string> = {
  dark: '深色',
  light: '浅色',
  claude: 'Claude 纸张',
}

export const THEME_DESC: Record<Theme, string> = {
  dark: '深色（默认）：深蓝黑背景 + 青色强调，原界面配色',
  light: '浅色：纯白面板 + 中性灰背景，保留青色强调',
  claude: 'Claude 纸张：暖白纸面 + 暖棕强调色',
}

/** 浏览器原生控件（滚动条 / 表单）的明暗。 */
export function themeColorScheme(theme: Theme): 'dark' | 'light' {
  return theme === 'dark' ? 'dark' : 'light'
}

export function isTheme(value: unknown): value is Theme {
  return typeof value === 'string' && (THEMES as readonly string[]).includes(value)
}

/** 主题循环顺序：dark → light → claude → dark（HeaderBar 快捷按钮用）。 */
export function nextTheme(theme: Theme): Theme {
  const index = THEMES.indexOf(theme)
  return THEMES[(index + 1) % THEMES.length]
}

/* ------------------------------------------------------------------ 环境降级 */

export type ThemeStorage = Pick<Storage, 'getItem' | 'setItem'>

export interface ThemeDeps {
  /** 显式传 null 表示不读写存储；省略则自动探测 localStorage */
  storage?: ThemeStorage | null
  /** 显式传 null 表示不操作 DOM；省略则自动探测 document.documentElement */
  root?: HTMLElement | null
}

function resolveStorage(): ThemeStorage | null {
  try {
    // node 环境下没有 localStorage；部分 WebView 访问时会抛 SecurityError
    if (typeof localStorage === 'undefined') return null
    return localStorage
  } catch {
    return null
  }
}

function resolveRoot(): HTMLElement | null {
  if (typeof document === 'undefined') return null
  return document.documentElement ?? null
}

function pickStorage(deps: ThemeDeps): ThemeStorage | null {
  return deps.storage === undefined ? resolveStorage() : deps.storage
}

function pickRoot(deps: ThemeDeps): HTMLElement | null {
  return deps.root === undefined ? resolveRoot() : deps.root
}

/* --------------------------------------------------------------------- 状态 */

const currentTheme = ref<Theme>(DEFAULT_THEME)

/** 当前生效主题（只读，组件模板里可直接用）。 */
export const activeTheme = readonly(currentTheme)

/** 读取持久化的主题；非法值 / 无存储时回落到 dark。 */
export function readStoredTheme(storage: ThemeStorage | null = resolveStorage()): Theme {
  try {
    const raw = storage?.getItem(THEME_STORAGE_KEY)
    return isTheme(raw) ? raw : DEFAULT_THEME
  } catch {
    return DEFAULT_THEME
  }
}

/** 当前生效主题（优先读 DOM 上的 data-theme，保证外部写入后也一致）。 */
export function getTheme(): Theme {
  const attr = resolveRoot()?.getAttribute('data-theme')
  return isTheme(attr) ? attr : currentTheme.value
}

/** 只把主题应用到 DOM（不写存储）；非法参数回落到 dark。返回实际应用的主题。 */
export function applyTheme(theme: Theme, root: HTMLElement | null = resolveRoot()): Theme {
  const next = isTheme(theme) ? theme : DEFAULT_THEME
  if (root) {
    root.setAttribute('data-theme', next)
    // 用 setProperty 而不是 style.colorScheme：对旧 WebView / TS lib 更稳
    root.style.setProperty('color-scheme', themeColorScheme(next))
  }
  currentTheme.value = next
  return next
}

/** 持久化并应用主题。返回实际生效的主题。 */
export function setTheme(theme: Theme, deps: ThemeDeps = {}): Theme {
  const next = isTheme(theme) ? theme : DEFAULT_THEME
  const storage = pickStorage(deps)
  try {
    storage?.setItem(THEME_STORAGE_KEY, next)
  } catch {
    // 存储不可用（隐私模式 / 配额满）：主题仍应立即生效
  }
  return applyTheme(next, pickRoot(deps))
}

/** 启动时调用：从 localStorage 恢复主题并应用（默认 dark）。 */
export function initTheme(deps: ThemeDeps = {}): Theme {
  return applyTheme(readStoredTheme(pickStorage(deps)), pickRoot(deps))
}
