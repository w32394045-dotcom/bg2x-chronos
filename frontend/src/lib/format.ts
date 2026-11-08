/** 展示层格式化工具（纯函数，便于单测）。 */

const BYTE_UNITS = ['B', 'KB', 'MB', 'GB', 'TB'] as const

/**
 * 字节数 → 人类可读字符串。
 * 非数字 / 负数 / 0 统一返回 "—"，避免 UI 出现 "0 B" 的误导。
 */
export function formatBytes(bytes: number | null | undefined, fractionDigits = 1): string {
  if (typeof bytes !== 'number' || !Number.isFinite(bytes) || bytes <= 0) return '—'
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < BYTE_UNITS.length - 1) {
    value /= 1024
    unit += 1
  }
  const digits = unit === 0 ? 0 : fractionDigits
  return `${value.toFixed(digits)} ${BYTE_UNITS[unit]}`
}

/**
 * 毫秒 → 紧凑时长。`950` → "0.9s"，`62_400` → "1m 02s"，`3_725_000` → "1h 02m"。
 */
export function formatDuration(ms: number | null | undefined): string {
  if (typeof ms !== 'number' || !Number.isFinite(ms) || ms < 0) return '—'
  if (ms < 1000) return `${Math.round(ms)}ms`
  const totalSeconds = ms / 1000
  if (totalSeconds < 60) return `${totalSeconds.toFixed(1)}s`
  const wholeSeconds = Math.round(totalSeconds)
  const minutes = Math.floor(wholeSeconds / 60)
  const seconds = wholeSeconds % 60
  if (minutes < 60) return `${minutes}m ${String(seconds).padStart(2, '0')}s`
  const hours = Math.floor(minutes / 60)
  return `${hours}h ${String(minutes % 60).padStart(2, '0')}m`
}

/** 0..100 的百分比文本，`total` 为 0 时返回 "0%"。 */
export function formatPercent(done: number, total: number): string {
  if (!Number.isFinite(done) || !Number.isFinite(total) || total <= 0) return '0%'
  const pct = Math.max(0, Math.min(100, (done / total) * 100))
  return `${pct >= 10 ? Math.round(pct) : pct.toFixed(1)}%`
}

/** unix ms → 本地 HH:mm:ss（首帧/非法值显示 "—"）。 */
export function formatClock(ms: number | null | undefined): string {
  if (typeof ms !== 'number' || !Number.isFinite(ms) || ms <= 0) return '—'
  const d = new Date(ms)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

/** 毫秒时间戳 → 相对 "刚刚 / 12s 前 / 3m 前 / 1h 前"。 */
export function formatRelative(ms: number | null | undefined, now = Date.now()): string {
  if (typeof ms !== 'number' || !Number.isFinite(ms) || ms <= 0) return '—'
  const diff = Math.max(0, now - ms)
  if (diff < 5000) return '刚刚'
  if (diff < 60_000) return `${Math.round(diff / 1000)}s 前`
  if (diff < 3_600_000) return `${Math.round(diff / 60_000)}m 前`
  if (diff < 86_400_000) return `${Math.round(diff / 3_600_000)}h 前`
  return `${Math.round(diff / 86_400_000)}d 前`
}

/** 保留 1 位小数的数字（0 或非法值 → "—"）。 */
export function formatNumber(value: number | null | undefined, digits = 0): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '—'
  return value.toFixed(digits)
}
