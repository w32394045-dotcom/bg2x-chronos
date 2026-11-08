import { describe, expect, it } from 'vitest'
import {
  normalizeEvent,
  normalizeInhibit,
  normalizeJob,
  normalizeLog,
  normalizeProgress,
} from '../events'

describe('normalizeProgress', () => {
  it('空 payload 收敛为可安全渲染的 progress 事件', () => {
    const event = normalizeProgress({})
    expect(event.kind).toBe('progress')
    expect(event.jobId).toBe('')
    expect(event.done).toBe(0)
    expect(event.total).toBe(0)
    expect(event.elapsedMs).toBe(0)
  })

  it('elapsedMs 缺失 / 字符串数字 / 负数都被收敛', () => {
    expect(normalizeProgress({ elapsedMs: undefined }).elapsedMs).toBe(0)
    expect(normalizeProgress({ elapsedMs: '1234' }).elapsedMs).toBe(1234)
    expect(normalizeProgress({ elapsedMs: -5 }).elapsedMs).toBe(0)
    expect(normalizeProgress({ elapsedMs: Number.NaN }).elapsedMs).toBe(0)
  })

  it('未知 kind 回退为 progress，done 不超过 total', () => {
    expect(normalizeProgress({ kind: 'weird' }).kind).toBe('progress')
    expect(normalizeProgress({ kind: 'done' }).kind).toBe('done')
    const clipped = normalizeProgress({ done: 99, total: 30 })
    expect(clipped.done).toBe(30)
    expect(clipped.total).toBe(30)
  })

  it('保留文件与输出路径字符串', () => {
    const event = normalizeProgress({
      file: 'C:\\Pics\\a.jpg',
      output: 'C:\\Pics\\a_chronos_x4.png',
      width: '4000',
      height: 3000,
    })
    expect(event.file).toBe('C:\\Pics\\a.jpg')
    expect(event.output).toBe('C:\\Pics\\a_chronos_x4.png')
    expect(event.width).toBe(4000)
    expect(event.height).toBe(3000)
  })
})

describe('normalizeJob / normalizeLog / normalizeInhibit', () => {
  it('job 缺失字段使用零值', () => {
    const job = normalizeJob({ jobId: 'job-1', total: 3, completed: 2 })
    expect(job.jobId).toBe('job-1')
    expect(job.completed).toBe(2)
    expect(job.failed).toBe(0)
    expect(job.cancelled).toBe(false)
  })

  it('log 级别非法时回退 info，并补本地时间', () => {
    const log = normalizeLog({ level: 'nope', msg: 'hello' })
    expect(log.level).toBe('info')
    expect(log.msg).toBe('hello')
    expect(log.timeMs).toBeGreaterThan(0)
    expect(normalizeLog({ level: 'warn', timeMs: 1234 }).timeMs).toBe(1234)
  })

  it('inhibit 未激活时 sinceMs 必须为 0', () => {
    const inactive = normalizeInhibit({ active: false, sleep: true, sinceMs: 999 })
    expect(inactive.active).toBe(false)
    expect(inactive.sinceMs).toBe(0)
    const active = normalizeInhibit({ active: true, sleep: true, sinceMs: 999 })
    expect(active.active).toBe(true)
    expect(active.sleep).toBe(true)
    expect(active.sinceMs).toBe(999)
    expect(active.driver).toBe('unsupported')
  })

  it('normalizeEvent 按事件名分派，未知事件原样返回', () => {
    const raw = { hello: 'world' }
    expect(normalizeEvent('chronos:unknown', raw)).toBe(raw)
    const snapshot = normalizeEvent('chronos:progress', { kind: 'failed', error: 'E' }) as {
      kind: string
      error: string
    }
    expect(snapshot.kind).toBe('failed')
    expect(snapshot.error).toBe('E')
  })
})
