/** 展示文案与静态样式映射（Tailwind 需要静态类名，不能拼接）。 */

import type { ItemStatus } from './queue'

export const MODEL_KIND_LABEL: Record<string, string> = {
  photo: '照片',
  anime: '动漫',
  compressed: '压缩图',
  general: '通用',
}

export const MODEL_KIND_HINT: Record<string, string> = {
  photo: '真实照片 / 人像细节修复',
  anime: '线条与色块锐化，抗振铃',
  compressed: '强去块效应，适合 JPEG 低质量源',
  general: '通用放大，兼顾噪点与纹理',
}

export const MODEL_KIND_CLASS: Record<string, string> = {
  photo: 'bg-sky-500/10 text-sky-300 ring-sky-400/25',
  anime: 'bg-fuchsia-500/10 text-fuchsia-300 ring-fuchsia-400/25',
  compressed: 'bg-amber-500/10 text-amber-300 ring-amber-400/25',
  general: 'bg-emerald-500/10 text-emerald-300 ring-emerald-400/25',
}

export function modelKindLabel(kind: string): string {
  return MODEL_KIND_LABEL[kind] ?? (kind || '未知')
}

export function modelKindClass(kind: string): string {
  return MODEL_KIND_CLASS[kind] ?? 'bg-overlay/5 text-mute ring-overlay/10'
}

export const STATUS_LABEL: Record<ItemStatus, string> = {
  queued: '排队中',
  running: '处理中',
  done: '已完成',
  failed: '失败',
  cancelled: '已取消',
  skipped: '已跳过',
}

export const STATUS_CLASS: Record<ItemStatus, string> = {
  queued: 'bg-overlay/5 text-mute ring-overlay/10',
  running: 'bg-brand-500/10 text-brand-300 ring-brand-400/30',
  done: 'bg-emerald-500/10 text-emerald-300 ring-emerald-400/25',
  failed: 'bg-rose-500/10 text-rose-300 ring-rose-400/25',
  cancelled: 'bg-amber-500/10 text-amber-300 ring-amber-400/25',
  skipped: 'bg-overlay/5 text-mute-soft ring-overlay/10',
}

export const LOG_LEVEL_CLASS: Record<string, string> = {
  debug: 'text-mute-soft',
  info: 'text-mute-strong',
  warn: 'text-amber-300',
  error: 'text-rose-300',
}

/** 控制台每条日志的行首标签（等宽，三字符对齐）。 */
export const LOG_LEVEL_LABEL: Record<string, { tag: string; text: string }> = {
  debug: { tag: 'DBG', text: 'text-mute-soft' },
  info: { tag: 'INF', text: 'text-mute-strong' },
  warn: { tag: 'WRN', text: 'text-amber-300' },
  error: { tag: 'ERR', text: 'text-rose-300' },
}

export function backendLabel(backend: string): string {
  switch (backend) {
    case 'openvino':
      return 'OpenVINO'
    case 'stub':
      return 'Stub · Lanczos'
    case 'go-lanczos':
      return 'Go · Lanczos'
    case 'windows-execution-state':
      return 'Windows ExecutionState'
    case 'linux-logind+screen-saver':
      return 'logind + ScreenSaver'
    case 'browser-mock':
      return '浏览器模拟'
    case 'unsupported':
      return '不受支持'
    default:
      return backend || '未知'
  }
}

/** 并发越界提示阈值（契约 §0 第 5 条：并发上限 8，UI 双重约束）。 */
export const CONCURRENCY_SOFT_LIMIT = 4
export const CONCURRENCY_HARD_LIMIT = 8
