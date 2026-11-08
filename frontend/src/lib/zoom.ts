/**
 * 界面缩放：用 CSS `zoom` 作用于应用根节点 `#app`。
 *
 * 为什么不用 root font-size：整套 UI 的字号都是绝对 px（text-[12px]…），
 * 改 html font-size 不会缩放任何字；CSS zoom 会同时缩放布局尺寸与文字。
 *
 * 范围 0.8–1.5，步进 0.1，持久化在 localStorage（key：`chronos.zoom`，默认 1）。
 * 快捷映射：
 * - 桌面（Wails）：Ctrl + = / Ctrl + - / Ctrl + 0（重置）
 * - 浏览器（HTTP / mock）：Ctrl + Shift + = / Ctrl + Shift + - / Ctrl + Shift + 0
 *   浏览器模式下 Ctrl +/- 通常被浏览器页面缩放占用，页面未必能拦截，故提供 Shift 变体。
 * 只在本模块识别到快捷键时才 preventDefault。
 */
import { readonly, ref } from 'vue'
import { detectTransport, type Transport } from './http'

export const ZOOM_MIN = 0.8
export const ZOOM_MAX = 1.5
export const ZOOM_STEP = 0.1
export const DEFAULT_ZOOM = 1
export const ZOOM_STORAGE_KEY = 'chronos.zoom'
export const APP_ROOT_ID = 'app'

export type ZoomCommand = 'in' | 'out' | 'reset'

const currentZoom = ref(DEFAULT_ZOOM)

/** 当前缩放（只读，SettingsModal 展示用）。 */
export const activeZoom = readonly(currentZoom)

/* ------------------------------------------------------------------ 数值工具 */

/** 把任意数值收拢到 [0.8, 1.5]，并四舍五入到 0.1 的整数倍。 */
export function clampZoom(value: number): number {
  if (!Number.isFinite(value)) return DEFAULT_ZOOM
  const rounded = Math.round(value * 10) / 10
  return Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, rounded))
}

/** 解析持久化值（字符串 / 数字 / 脏数据）；非法值回落到 1。 */
export function normalizeZoom(value: unknown): number {
  if (typeof value === 'number') return clampZoom(value)
  if (typeof value === 'string' && value.trim() !== '') return clampZoom(Number.parseFloat(value))
  return DEFAULT_ZOOM
}

/** 以 0.1 为步进缩放（纯函数，便于单测）。 */
export function stepZoom(current: number, steps: number): number {
  return clampZoom(normalizeZoom(current) + steps * ZOOM_STEP)
}

/* ------------------------------------------------------------------ 环境降级 */

export type ZoomStorage = Pick<Storage, 'getItem' | 'setItem'>

export interface ZoomDeps {
  /** 显式传 null 表示不读写存储；省略则自动探测 localStorage */
  storage?: ZoomStorage | null
  /** 显式传 null 表示不操作 DOM；省略则自动查找 #app */
  root?: HTMLElement | null
}

function resolveStorage(): ZoomStorage | null {
  try {
    if (typeof localStorage === 'undefined') return null
    return localStorage
  } catch {
    return null
  }
}

function resolveRoot(): HTMLElement | null {
  if (typeof document === 'undefined') return null
  return document.getElementById(APP_ROOT_ID)
}

function pickStorage(deps: ZoomDeps): ZoomStorage | null {
  return deps.storage === undefined ? resolveStorage() : deps.storage
}

function pickRoot(deps: ZoomDeps): HTMLElement | null {
  return deps.root === undefined ? resolveRoot() : deps.root
}

/* --------------------------------------------------------------------- 状态 */

/** 当前生效缩放。 */
export function getZoom(): number {
  return currentZoom.value
}

/** 读取持久化缩放；无存储 / 脏数据回落到 1。 */
export function readStoredZoom(storage: ZoomStorage | null = resolveStorage()): number {
  try {
    return normalizeZoom(storage?.getItem(ZOOM_STORAGE_KEY))
  } catch {
    return DEFAULT_ZOOM
  }
}

/** 只把缩放应用到 DOM（不写存储），返回实际生效值。 */
export function applyZoom(zoom: number, root: HTMLElement | null = resolveRoot()): number {
  const next = normalizeZoom(zoom)
  if (root) {
    // 用 setProperty：CSSStyleDeclaration.zoom 在各 TS lib 版本里类型不一致
    root.style.setProperty('zoom', String(next))
    root.setAttribute('data-zoom', String(next))
  }
  currentZoom.value = next
  return next
}

/** 持久化并应用缩放，返回实际生效值。 */
export function setZoom(zoom: number, deps: ZoomDeps = {}): number {
  const next = applyZoom(zoom, pickRoot(deps))
  const storage = pickStorage(deps)
  try {
    storage?.setItem(ZOOM_STORAGE_KEY, String(next))
  } catch {
    // 存储不可用：缩放仍立即生效
  }
  return next
}

/** 放大一档（0.1）并持久化。 */
export function increaseZoom(deps: ZoomDeps = {}): number {
  return setZoom(stepZoom(currentZoom.value, 1), deps)
}

/** 缩小一档（0.1）并持久化。 */
export function decreaseZoom(deps: ZoomDeps = {}): number {
  return setZoom(stepZoom(currentZoom.value, -1), deps)
}

/** 重置为 100% 并持久化。 */
export function resetZoom(deps: ZoomDeps = {}): number {
  return setZoom(DEFAULT_ZOOM, deps)
}

/** 启动时调用：从 localStorage 恢复缩放并应用（默认 100%）。 */
export function initZoom(deps: ZoomDeps = {}): number {
  return applyZoom(readStoredZoom(pickStorage(deps)), pickRoot(deps))
}

/* ------------------------------------------------------------------- 快捷键 */

export interface ZoomKeyEvent {
  key: string
  ctrlKey?: boolean
  metaKey?: boolean
  altKey?: boolean
  shiftKey?: boolean
  /** 键盘物理位置兜底（部分输入法 / 布局下 event.key 不稳定） */
  code?: string
}

const KEY_COMMAND: Record<string, ZoomCommand> = {
  '=': 'in',
  '+': 'in',
  '-': 'out',
  _: 'out',
  '0': 'reset',
  ')': 'reset',
}

const CODE_COMMAND: Record<string, ZoomCommand> = {
  Equal: 'in',
  NumpadAdd: 'in',
  Minus: 'out',
  NumpadSubtract: 'out',
  Digit0: 'reset',
  Numpad0: 'reset',
}

/**
 * 把一次按键映射为缩放命令；不匹配返回 null（调用方不得 preventDefault）。
 *
 * 桌面（wails）用不带 Shift 的组合，浏览器（http / mock）用带 Shift 的组合，
 * 避免和浏览器自身的页面缩放冲突（浏览器模式下 Ctrl +/- 可能被浏览器吞掉）。
 * 支持 Meta（macOS Cmd）作为 Ctrl 等价键。
 */
export function matchZoomShortcut(event: ZoomKeyEvent, transport: Transport): ZoomCommand | null {
  const ctrl = event.ctrlKey === true || event.metaKey === true
  if (!ctrl || event.altKey === true) return null

  const browser = transport !== 'wails'
  if (browser) {
    if (event.shiftKey !== true) return null
  } else if (event.shiftKey === true) {
    return null
  }

  const byKey = KEY_COMMAND[event.key]
  if (byKey) return byKey
  return event.code ? (CODE_COMMAND[event.code] ?? null) : null
}

/** 焦点在可输入元素里时不响应缩放快捷键。 */
export function isEditableTarget(target: unknown): boolean {
  if (!target || typeof target !== 'object') return false
  const element = target as { tagName?: unknown; isContentEditable?: unknown }
  const tag = typeof element.tagName === 'string' ? element.tagName.toUpperCase() : ''
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true
  return element.isContentEditable === true
}

/** SettingsModal 里的快捷键提示文案。 */
export function getZoomShortcutHint(transport: Transport): string {
  return transport === 'wails'
    ? 'Ctrl + = / Ctrl + - / Ctrl + 0（重置）'
    : 'Ctrl + Shift + = / Ctrl + Shift + - / Ctrl + Shift + 0（重置）'
}

export interface ZoomShortcutOptions {
  transport?: Transport
  /** 默认 window；单测可注入 fake EventTarget */
  target?: Window | null
  root?: HTMLElement | null
}

/**
 * 安装全局缩放快捷键，返回卸载函数。
 * 仅在命中快捷键时 preventDefault，且输入框 / textarea / contenteditable 内忽略。
 */
export function installZoomShortcuts(options: ZoomShortcutOptions = {}): () => void {
  const transport = options.transport ?? detectTransport()
  const target =
    options.target === undefined ? (typeof window === 'undefined' ? null : window) : options.target
  if (!target) return () => {}

  const deps: ZoomDeps = options.root === undefined ? {} : { root: options.root }

  function onKeydown(event: KeyboardEvent): void {
    if (event.defaultPrevented) return
    if (isEditableTarget(event.target)) return
    const command = matchZoomShortcut(event, transport)
    if (!command) return
    event.preventDefault()
    if (command === 'in') increaseZoom(deps)
    else if (command === 'out') decreaseZoom(deps)
    else resetZoom(deps)
  }

  target.addEventListener('keydown', onKeydown as EventListener)
  return () => target.removeEventListener('keydown', onKeydown as EventListener)
}
