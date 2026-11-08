import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { api, isDesktop, onEvent } from '../api'
import { configureMock, resetMock } from '../mock'
import type { BatchRequest, JobSummary, ProgressEvent } from '../types'

function request(files: string[]): BatchRequest {
  return {
    files,
    modelId: '',
    modelPath: '',
    scale: 4,
    outputDir: '',
    concurrency: 4,
    tileSize: 256,
    overlap: 16,
    threads: 0,
    fast: false,
    inhibit: null,
  }
}

describe('mock api（浏览器预览后端）', () => {
  beforeEach(() => {
    resetMock()
    configureMock({ tickMs: 1 })
  })

  afterEach(() => {
    resetMock()
  })

  it('node 环境下 isDesktop() 为 false，自动走 mock', () => {
    expect(isDesktop()).toBe(false)
  })

  it('GetAppInfo / ListModels 返回契约 §3.2 的字段形状', async () => {
    const info = await api.GetAppInfo()
    expect(info.engine.backend).toBe('stub')
    expect(info.maxConcurrency).toBe(8)
    expect(typeof info.dataDir).toBe('string')
    expect(info.mode).toBe('desktop')
    expect(info.browseSupported).toBe(true)

    const models = await api.ListModels()
    expect(models.length).toBeGreaterThan(0)
    expect(models.every((model) => typeof model.id === 'string' && typeof model.scale === 'number')).toBe(true)
    expect(models.every((model) => typeof model.installed === 'boolean')).toBe(true)
    expect(models.some((model) => model.installed)).toBe(true)
  })

  it('ListDirectory 返回目录列表（PathPicker 预览数据源）', async () => {
    const home = await api.ListDirectory('')
    expect(home.path).toBe('C:\\Users\\Public\\Pictures')
    expect(Array.isArray(home.entries)).toBe(true)
    expect(home.entries.some((entry) => entry.isDir)).toBe(true)
    expect(home.entries.some((entry) => entry.isImage)).toBe(true)
    expect(home.entries.every((entry) => typeof entry.path === 'string' && typeof entry.size === 'number')).toBe(true)

    const sub = await api.ListDirectory('C:\\Users\\Public\\Pictures\\风景')
    expect(sub.entries.length).toBeGreaterThan(0)
    expect(sub.parent).toBe('C:\\Users\\Public\\Pictures')

    // 未知目录：空列表但 parent 可用，UI 仍能上溯
    const unknown = await api.ListDirectory('C:\\nowhere')
    expect(unknown.entries).toEqual([])
    expect(unknown.parent).toBe('C:\\')
  })

  it('SetConcurrency 校验 1..8，GetConcurrency 同步生效', async () => {
    await expect(api.SetConcurrency(9)).rejects.toThrow(/1~8/)
    await expect(api.SetConcurrency(0)).rejects.toThrow(/1~8/)
    await api.SetConcurrency(3)
    expect(await api.GetConcurrency()).toBe(3)
  })

  it('SetInhibit / GetInhibitStatus 维护激活状态与 sinceMs', async () => {
    const active = await api.SetInhibit({ sleep: true, display: false, lock: false, autoRelease: true })
    expect(active.active).toBe(true)
    expect(active.sleep).toBe(true)
    expect(active.sinceMs).toBeGreaterThan(0)
    expect((await api.GetInhibitStatus()).sleep).toBe(true)

    const released = await api.SetInhibit({ sleep: false, display: false, lock: false, autoRelease: true })
    expect(released.active).toBe(false)
    expect(released.sinceMs).toBe(0)
  })

  it('UpscaleBatch 产生 queued/started/progress/done 事件与 job 汇总', async () => {
    const events: ProgressEvent[] = []
    const offProgress = onEvent('chronos:progress', (event) => events.push(event))
    let resolveJob: (summary: JobSummary) => void = () => {}
    const jobDone = new Promise<JobSummary>((resolve) => {
      resolveJob = resolve
    })
    const offJob = onEvent('chronos:job', (summary) => {
      offJob()
      resolveJob(summary)
    })

    const result = await api.UpscaleBatch(request(['C:\\Pics\\a.jpg', 'C:\\Pics\\b.jpg']))
    expect(result.jobId).toMatch(/^mock-job-/)
    expect(result.queued).toBe(2)
    expect(result.skipped).toEqual([])

    const summary = await jobDone
    expect(summary.jobId).toBe(result.jobId)
    expect(summary.total).toBe(2)
    expect(summary.completed).toBe(2)
    expect(summary.failed).toBe(0)
    expect(summary.cancelled).toBe(false)

    expect(events.length).toBeGreaterThan(0)
    expect(events.some((event) => event.kind === 'started')).toBe(true)
    expect(events.some((event) => event.kind === 'progress')).toBe(true)
    expect(events.some((event) => event.kind === 'done' && event.output.endsWith('_chronos_x4.png'))).toBe(true)
    expect(events.every((event) => event.jobId === result.jobId)).toBe(true)
    offProgress()
  })

  it('CancelJob 对未知任务返回 false，对当前任务返回 true 并推 cancelled 事件', async () => {
    expect(await api.CancelJob('not-a-job')).toBe(false)

    const kinds: string[] = []
    const off = onEvent('chronos:progress', (event) => kinds.push(event.kind))
    const result = await api.UpscaleBatch(request(['C:\\Pics\\slow.jpg']))
    expect(await api.CancelJob(result.jobId)).toBe(true)
    await new Promise((resolve) => setTimeout(resolve, 20))
    expect(kinds).toContain('cancelled')
    off()
  })

  it('PickImages / PickOutputDir 返回可用于预览的路径', async () => {
    const images = await api.PickImages()
    expect(images.length).toBeGreaterThan(0)
    expect(images.every((path) => /^[A-Za-z]:\\/.test(path))).toBe(true)
    expect(await api.PickOutputDir()).toMatch(/^[A-Za-z]:\\/)
  })
})
