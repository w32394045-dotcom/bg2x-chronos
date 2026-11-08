import { describe, expect, it } from 'vitest'
import { formatBytes, formatClock, formatDuration, formatPercent, formatRelative } from '../format'

describe('formatBytes', () => {
  it('对空值 / 0 / 负数返回占位符', () => {
    expect(formatBytes(undefined)).toBe('—')
    expect(formatBytes(null)).toBe('—')
    expect(formatBytes(0)).toBe('—')
    expect(formatBytes(-1024)).toBe('—')
  })

  it('按 1024 进制换算并保留 1 位小数', () => {
    expect(formatBytes(512)).toBe('512 B')
    expect(formatBytes(1536)).toBe('1.5 KB')
    expect(formatBytes(5 * 1024 * 1024)).toBe('5.0 MB')
    expect(formatBytes(63.9 * 1024 * 1024)).toBe('63.9 MB')
  })
})

describe('formatDuration', () => {
  it('毫秒 / 秒 / 分 / 时的分级文本', () => {
    expect(formatDuration(0)).toBe('0ms')
    expect(formatDuration(950)).toBe('950ms')
    expect(formatDuration(12_345)).toBe('12.3s')
    expect(formatDuration(65_000)).toBe('1m 05s')
    expect(formatDuration(3_661_000)).toBe('1h 01m')
  })

  it('非法输入返回占位符', () => {
    expect(formatDuration(-1)).toBe('—')
    expect(formatDuration(Number.NaN)).toBe('—')
  })
})

describe('formatPercent / formatClock / formatRelative', () => {
  it('百分比裁剪到 0..100', () => {
    expect(formatPercent(5, 10)).toBe('50%')
    expect(formatPercent(0, 0)).toBe('0%')
    expect(formatPercent(200, 100)).toBe('100%')
  })

  it('时间戳为 0 时显示占位符，否则为 HH:mm:ss', () => {
    expect(formatClock(0)).toBe('—')
    expect(formatClock(Date.UTC(2026, 0, 1, 12, 34, 56))).toMatch(/^\d{2}:\d{2}:\d{2}$/)
  })

  it('相对时间分档', () => {
    const now = 1_700_000_000_000
    expect(formatRelative(now - 1000, now)).toBe('刚刚')
    expect(formatRelative(now - 30_000, now)).toBe('30s 前')
    expect(formatRelative(now - 120_000, now)).toBe('2m 前')
    expect(formatRelative(now - 7_200_000, now)).toBe('2h 前')
  })
})
