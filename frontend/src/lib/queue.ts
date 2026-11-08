/**
 * 队列纯函数（无 Vue 依赖，可直接单测）。
 *
 * 队列元素是 UI 自己的投影：以 `taskId || filePath` 作为稳定 key 做合并，
 * 进度事件按 key 覆盖，而不是追加，保证 500ms 一次的高频事件不会撑爆列表。
 */
import type { ProgressEvent } from './types'

export type ItemStatus = 'queued' | 'running' | 'done' | 'failed' | 'cancelled' | 'skipped'

export interface QueueItem {
  /** 稳定 key：优先 taskId，其次规范化后的文件路径 */
  id: string
  taskId: string
  /** 原始绝对路径（Windows 反斜杠） */
  file: string
  /** 展示用文件名 */
  name: string
  /** 所在目录（用于 reveal / 展示） */
  dir: string
  status: ItemStatus
  done: number
  total: number
  output: string
  width: number
  height: number
  error: string
  backend: string
  elapsedMs: number
  /** 入队序号（0-based），与 ProgressEvent.index 对齐 */
  index: number
}

export interface QueueSummary {
  total: number
  queued: number
  running: number
  done: number
  failed: number
  cancelled: number
  skipped: number
  /** 已完成任务占比 0..1 */
  progress: number
}

/** 路径规范化 key：Windows 不区分大小写，且 `\` 与 `/` 等价。 */
export function pathKey(path: string): string {
  return path.trim().replace(/\//g, '\\').toLowerCase()
}

/** 取文件名（兼容 `\` 与 `/`）。 */
export function fileLabel(path: string): string {
  const trimmed = path.trim().replace(/[\\/]+$/, '')
  const idx = Math.max(trimmed.lastIndexOf('\\'), trimmed.lastIndexOf('/'))
  return idx >= 0 ? trimmed.slice(idx + 1) : trimmed
}

/** 取目录部分（无目录分隔符时返回空串）。 */
export function fileDir(path: string): string {
  const trimmed = path.trim().replace(/[\\/]+$/, '')
  const idx = Math.max(trimmed.lastIndexOf('\\'), trimmed.lastIndexOf('/'))
  return idx >= 0 ? trimmed.slice(0, idx) : ''
}

/** 用路径构造队列项。 */
export function makeItem(file: string, index = 0): QueueItem {
  return {
    id: pathKey(file),
    taskId: '',
    file,
    name: fileLabel(file),
    dir: fileDir(file),
    status: 'queued',
    done: 0,
    total: 0,
    output: '',
    width: 0,
    height: 0,
    error: '',
    backend: '',
    elapsedMs: 0,
    index,
  }
}

export interface EnqueueResult {
  items: QueueItem[]
  /** 本次真正新增的项 */
  added: QueueItem[]
  /** 因为重复（同路径已在队列/已完成）而跳过的路径 */
  duplicates: string[]
}

/**
 * 入队 + 去重。不修改入参数组。
 * 去重规则：同一路径只允许存在于队列中一次（含已完成项，避免重复处理同一文件）。
 */
export function enqueue(items: readonly QueueItem[], files: readonly string[]): EnqueueResult {
  const seen = new Set(items.map((item) => item.id))
  const added: QueueItem[] = []
  const duplicates: string[] = []
  const next = items.slice()
  for (const file of files) {
    const trimmed = typeof file === 'string' ? file.trim() : ''
    if (!trimmed) continue
    const key = pathKey(trimmed)
    if (seen.has(key)) {
      duplicates.push(trimmed)
      continue
    }
    seen.add(key)
    const item = makeItem(trimmed, next.length)
    next.push(item)
    added.push(item)
  }
  return { items: next, added, duplicates }
}

function findIndexByKey(items: readonly QueueItem[], key: string): number {
  return items.findIndex((item) => item.id === key || (item.taskId !== '' && item.taskId === key))
}

/** 事件 → 队列项状态机：queued/started/progress/done/failed/cancelled。 */
function applyToItem(item: QueueItem, ev: ProgressEvent): QueueItem {
  const next: QueueItem = { ...item }
  if (ev.taskId) next.taskId = ev.taskId
  if (ev.backend) next.backend = ev.backend
  if (ev.elapsedMs > 0) next.elapsedMs = ev.elapsedMs
  if (ev.total > 0) next.total = ev.total
  if (ev.done > 0) next.done = Math.min(ev.done, next.total > 0 ? next.total : ev.done)

  switch (ev.kind) {
    case 'queued':
      next.status = 'queued'
      break
    case 'started':
      next.status = 'running'
      next.done = 0
      next.error = ''
      break
    case 'progress':
      next.status = 'running'
      break
    case 'done':
      next.status = 'done'
      next.done = ev.total > 0 ? ev.total : next.done
      if (next.total === 0 && ev.total > 0) next.total = ev.total
      next.error = ''
      if (ev.output) next.output = ev.output
      if (ev.width > 0) next.width = ev.width
      if (ev.height > 0) next.height = ev.height
      break
    case 'failed':
      next.status = 'failed'
      next.error = ev.error || '处理失败'
      break
    case 'cancelled':
      next.status = 'cancelled'
      next.error = ev.error || ''
      break
    default:
      break
  }
  return next
}

/**
 * 合并一个进度事件，返回新数组（触发 Vue 响应式更新）。
 * 若事件对应的项不在队列里（例如刷新后收到迟到事件），按事件内容补建一条，
 * 保证结果不丢失；补建项的 id 使用 taskId 或路径 key。
 */
export function applyProgress(items: readonly QueueItem[], ev: ProgressEvent): QueueItem[] {
  const key = ev.taskId || pathKey(ev.file)
  if (!key) return items.slice()
  // 优先按 taskId 合并；taskId 尚未登记（例如 started 事件丢失/迟到）时回退到文件路径
  let idx = ev.taskId ? findIndexByKey(items, ev.taskId) : -1
  if (idx < 0 && ev.file) idx = findIndexByKey(items, pathKey(ev.file))
  if (idx >= 0) {
    const next = items.slice()
    next[idx] = applyToItem(items[idx], ev)
    return next
  }
  const base = ev.file
    ? makeItem(ev.file, ev.index)
    : {
        ...makeItem(`unknown-${key}`, ev.index),
        name: key,
      }
  const created: QueueItem = {
    ...base,
    id: key,
    taskId: ev.taskId,
    file: ev.file || base.file,
    name: ev.file ? fileLabel(ev.file) : base.name,
    dir: ev.file ? fileDir(ev.file) : '',
  }
  const merged = applyToItem(created, ev)
  // 保持事件到达顺序稳定：按 index 插入
  const next = items.slice()
  const insertAt = next.findIndex((item) => item.index > merged.index)
  if (insertAt < 0) next.push(merged)
  else next.splice(insertAt, 0, merged)
  return next
}

/** 按 id、taskId 或原始路径移除一项。 */
export function removeItem(items: readonly QueueItem[], id: string): QueueItem[] {
  const key = pathKey(id)
  return items.filter((item) => item.id !== key && item.id !== id && item.taskId !== id)
}

/** 清除所有终态项（done / failed / cancelled / skipped）。 */
export function clearFinished(items: readonly QueueItem[]): QueueItem[] {
  return items.filter((item) => item.status === 'queued' || item.status === 'running')
}

/** 队列汇总，供状态栏 / 主按钮计数使用。 */
export function summarize(items: readonly QueueItem[]): QueueSummary {
  const summary: QueueSummary = {
    total: items.length,
    queued: 0,
    running: 0,
    done: 0,
    failed: 0,
    cancelled: 0,
    skipped: 0,
    progress: 0,
  }
  for (const item of items) {
    switch (item.status) {
      case 'queued':
        summary.queued += 1
        break
      case 'running':
        summary.running += 1
        break
      case 'done':
        summary.done += 1
        break
      case 'failed':
        summary.failed += 1
        break
      case 'cancelled':
        summary.cancelled += 1
        break
      case 'skipped':
        summary.skipped += 1
        break
      default:
        break
    }
  }
  summary.progress = summary.total > 0 ? summary.done / summary.total : 0
  return summary
}

/** 是否还有可处理的输入（用于主按钮 disabled / 计数）。 */
export function pendingCount(items: readonly QueueItem[]): number {
  return items.filter((item) => item.status === 'queued').length
}

/** 单项完成百分比 0..1。 */
export function itemPercent(item: QueueItem): number {
  if (item.status === 'done') return 1
  if (item.total <= 0) return 0
  return Math.max(0, Math.min(1, item.done / item.total))
}
