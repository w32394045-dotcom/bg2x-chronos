import { beforeEach, describe, expect, it } from 'vitest'
import { configureMock, mockApi, resetMock } from '@/lib/mock'
import { mockSubscribe } from '@/lib/eventbus'
import type { ModelPullState } from '@/lib/types'

describe('mock 模型管理（契约 v3）', () => {
  beforeEach(() => {
    resetMock()
    configureMock({ tickMs: 5 })
  })

  it('PullModel 先返回 queued，并持续推送 chronos:model 直到 done', async () => {
    const events: ModelPullState[] = []
    const off = mockSubscribe('chronos:model', (data) => events.push(data as ModelPullState))

    const models = await mockApi.ListModels()
    const id = models[0].id
    const initial = await mockApi.PullModel({ modelId: id, mirror: '' })
    expect(initial.phase).toBe('queued')
    expect(initial.total).toBeGreaterThan(0)

    await new Promise((resolve) => setTimeout(resolve, 200))
    off()

    expect(events.length).toBeGreaterThan(1)
    expect(events.every((event) => event.modelId === id)).toBe(true)
    expect(events[events.length - 1]?.phase).toBe('done')
    // 下载完成后 mock 会把模型标记为已安装 + 自检通过
    const after = (await mockApi.ListModels()).find((model) => model.id === id)
    expect(after?.installed).toBe(true)
    expect(after?.verified).toBe(true)
  })

  it('SelfTestModel 返回五项检查，GetAccelStatus 默认 CPU', async () => {
    const models = await mockApi.ListModels()
    const result = await mockApi.SelfTestModel(models[0].id)
    expect(result.ok).toBe(true)
    expect(result.checks.map((check) => check.name)).toEqual(['load', 'size', 'color', 'seam', 'perf'])

    const status = await mockApi.GetAccelStatus()
    expect(status.active.device).toBe('CPU')
    expect(status.available).toContain('CPU')
    expect(status.gpuUsable).toBe(false)

    const cpu = await mockApi.SetAccelMode('cpu')
    expect(cpu.mode).toBe('cpu')
    const auto = await mockApi.SetAccelMode('weird')
    expect(auto.mode).toBe('auto')
  })

  it('CancelPull 与 RemoveModel 会改变状态', async () => {
    const models = await mockApi.ListModels()
    const model = models[0]
    expect(await mockApi.CancelPull('does-not-exist')).toBe(false)

    await mockApi.PullModel({ modelId: model.id, mirror: '' })
    expect(await mockApi.CancelPull(model.id)).toBe(true)

    await mockApi.RemoveModel(model.id)
    const after = (await mockApi.ListModels()).find((m) => m.id === model.id)
    expect(after?.installed).toBe(false)
  })
})
