/**
 * Wails 事件 payload 归一化（契约 §3.3 / §5）。
 *
 * Go 侧 struct 零值会省略部分字段（`elapsedMs` 可能缺失或为 0），
 * 且 JSON 数字在极少数情况下可能以字符串出现，所以这里对所有字段做防御性收敛，
 * UI 只消费归一化后的结构，不再各自解构原始 payload。
 */
import type { InhibitStatus, JobSummary, LogEvent, ProgressEvent } from './types'

export const CHRONOS_EVENTS = {
  progress: 'chronos:progress',
  job: 'chronos:job',
  log: 'chronos:log',
  inhibit: 'chronos:inhibit',
} as const

export type ChronosEventName = (typeof CHRONOS_EVENTS)[keyof typeof CHRONOS_EVENTS]

const PROGRESS_KINDS = new Set(['queued', 'started', 'progress', 'done', 'failed', 'cancelled'])
const LOG_LEVELS = new Set(['debug', 'info', 'warn', 'error'])

function asRecord(raw: unknown): Record<string, unknown> {
  return raw && typeof raw === 'object' ? (raw as Record<string, unknown>) : {}
}

function str(value: unknown, fallback = ''): string {
  if (typeof value === 'string') return value
  if (typeof value === 'number' && Number.isFinite(value)) return String(value)
  return fallback
}

/** 整数收敛：非法值 / NaN / Infinity → fallback，负数按 0 处理（进度不能倒退成负）。 */
export function int(value: unknown, fallback = 0): number {
  const n = typeof value === 'number' ? value : Number(value)
  if (!Number.isFinite(n)) return fallback
  return Math.max(0, Math.trunc(n))
}

function bool(value: unknown): boolean {
  if (typeof value === 'boolean') return value
  if (typeof value === 'number') return value !== 0
  if (typeof value === 'string') return value === 'true' || value === '1'
  return false
}

/** 进度事件：字段缺失/类型漂移一律收敛为可直接参与 UI 计算的形态。 */
export function normalizeProgress(raw: unknown): ProgressEvent {
  const r = asRecord(raw)
  const kind = str(r.kind, 'progress')
  const total = int(r.total, 0)
  let done = int(r.done, 0)
  // done 不得超过 total（Go 用原子自增，理论上不会越界；防御 UI 进度条溢出）
  if (total > 0 && done > total) done = total
  return {
    jobId: str(r.jobId),
    taskId: str(r.taskId),
    file: str(r.file),
    kind: PROGRESS_KINDS.has(kind) ? kind : 'progress',
    index: int(r.index, 0),
    done,
    total,
    output: str(r.output),
    width: int(r.width, 0),
    height: int(r.height, 0),
    error: str(r.error),
    backend: str(r.backend),
    elapsedMs: int(r.elapsedMs, 0),
  }
}

/** job 汇总事件。 */
export function normalizeJob(raw: unknown): JobSummary {
  const r = asRecord(raw)
  return {
    jobId: str(r.jobId),
    total: int(r.total, 0),
    completed: int(r.completed, 0),
    failed: int(r.failed, 0),
    cancelled: bool(r.cancelled),
    elapsedMs: int(r.elapsedMs, 0),
  }
}

/** 日志事件：时间戳缺失时补本地时间，保证控制台排序稳定。 */
export function normalizeLog(raw: unknown): LogEvent {
  const r = asRecord(raw)
  const level = str(r.level, 'info')
  const timeMs = int(r.timeMs, 0)
  return {
    level: LOG_LEVELS.has(level) ? level : 'info',
    msg: str(r.msg),
    timeMs: timeMs > 0 ? timeMs : Date.now(),
  }
}

/** 阻止睡眠状态。未激活时 sinceMs 必须为 0（契约 §3.2）。 */
export function normalizeInhibit(raw: unknown): InhibitStatus {
  const r = asRecord(raw)
  const active = bool(r.active)
  return {
    active,
    sleep: bool(r.sleep),
    display: bool(r.display),
    lock: bool(r.lock),
    sleepSupported: bool(r.sleepSupported),
    displaySupported: bool(r.displaySupported),
    lockSupported: bool(r.lockSupported),
    driver: str(r.driver, 'unsupported'),
    detail: str(r.detail),
    sinceMs: active ? int(r.sinceMs, 0) : 0,
  }
}

export type EventPayloadMap = {
  'chronos:progress': ProgressEvent
  'chronos:job': JobSummary
  'chronos:log': LogEvent
  'chronos:inhibit': InhibitStatus
}

/** 按事件名选择归一化函数；未知事件原样透传。 */
export function normalizeEvent(name: string, raw: unknown): unknown {
  switch (name) {
    case CHRONOS_EVENTS.progress:
      return normalizeProgress(raw)
    case CHRONOS_EVENTS.job:
      return normalizeJob(raw)
    case CHRONOS_EVENTS.log:
      return normalizeLog(raw)
    case CHRONOS_EVENTS.inhibit:
      return normalizeInhibit(raw)
    default:
      return raw
  }
}
