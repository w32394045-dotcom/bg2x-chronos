import { describe, expect, it } from 'vitest'
import {
  applyProgress,
  clearFinished,
  enqueue,
  fileLabel,
  itemPercent,
  pathKey,
  removeItem,
  summarize,
  type QueueItem,
} from '../queue'
import type { ProgressEvent } from '../types'

function event(partial: Partial<ProgressEvent>): ProgressEvent {
  return {
    jobId: 'job-1',
    taskId: 'task-1',
    file: 'C:\\Pics\\a.jpg',
    kind: 'progress',
    index: 0,
    done: 0,
    total: 0,
    output: '',
    width: 0,
    height: 0,
    error: '',
    backend: '',
    elapsedMs: 0,
    ...partial,
  }
}

function files(): string[] {
  return ['C:\\Pics\\a.jpg', 'C:\\Pics\\b.jpg']
}

describe('enqueue', () => {
  it('入队后生成稳定的路径 key 与展示名', () => {
    const result = enqueue([], files())
    expect(result.items).toHaveLength(2)
    expect(result.added).toHaveLength(2)
    expect(result.items[0].name).toBe('a.jpg')
    expect(result.items[0].dir).toBe('C:\\Pics')
    expect(result.items[0].status).toBe('queued')
    expect(result.duplicates).toHaveLength(0)
  })

  it('按不区分大小写 + 斜杠归一化去重，且不修改入参数组', () => {
    const first = enqueue([], ['C:\\Pics\\a.jpg'])
    const snapshot = first.items.slice()
    const second = enqueue(first.items, ['c:/pics/A.JPG', 'C:\\Pics\\b.png', '  '])
    expect(second.added).toHaveLength(1)
    expect(second.added[0].name).toBe('b.png')
    expect(second.duplicates).toEqual(['c:/pics/A.JPG'])
    expect(second.items.map((item) => item.name)).toEqual(['a.jpg', 'b.png'])
    expect(first.items).toEqual(snapshot)
  })

  it('路径工具函数兼容两种分隔符', () => {
    expect(pathKey('C:/Pics/A.JPG')).toBe('c:\\pics\\a.jpg')
    expect(fileLabel('/home/user/图 1.png')).toBe('图 1.png')
  })
})

describe('applyProgress', () => {
  function seeded(): QueueItem[] {
    return enqueue([], files()).items
  }

  it('按 taskId 合并 started → progress → done，不追加新行', () => {
    let items = seeded()
    items = applyProgress(items, event({ kind: 'started', taskId: 't-1', total: 9 }))
    expect(items).toHaveLength(2)
    expect(items[0].status).toBe('running')
    expect(items[0].taskId).toBe('t-1')
    expect(items[0].total).toBe(9)

    items = applyProgress(items, event({ kind: 'progress', taskId: 't-1', done: 4, total: 9, elapsedMs: 1200 }))
    expect(items[0].done).toBe(4)
    expect(items[0].elapsedMs).toBe(1200)
    expect(itemPercent(items[0])).toBeCloseTo(4 / 9, 5)

    items = applyProgress(
      items,
      event({
        kind: 'done',
        taskId: 't-1',
        done: 9,
        total: 9,
        output: 'C:\\Pics\\a_chronos_x4.png',
        width: 4000,
        height: 3000,
        backend: 'openvino',
      }),
    )
    expect(items[0].status).toBe('done')
    expect(items[0].output).toBe('C:\\Pics\\a_chronos_x4.png')
    expect(items[0].width).toBe(4000)
    expect(items[0].height).toBe(3000)
    expect(items[0].backend).toBe('openvino')
    expect(itemPercent(items[0])).toBe(1)
  })

  it('taskId 为空时按文件路径回退匹配', () => {
    let items = seeded()
    items = applyProgress(items, event({ kind: 'progress', taskId: '', file: 'C:\\Pics\\b.jpg', done: 2, total: 8 }))
    expect(items[1].status).toBe('running')
    expect(items[1].done).toBe(2)
    expect(items).toHaveLength(2)
  })

  it('failed 写入错误信息且保留其它字段', () => {
    let items = seeded()
    items = applyProgress(items, event({ kind: 'failed', taskId: 't-2', file: 'C:\\Pics\\b.jpg', error: 'CHRONOS_ERR_IO', elapsedMs: 800 }))
    expect(items[1].status).toBe('failed')
    expect(items[1].error).toBe('CHRONOS_ERR_IO')
    expect(items[1].elapsedMs).toBe(800)
  })

  it('cancelled 标注取消状态', () => {
    let items = seeded()
    items = applyProgress(items, event({ kind: 'cancelled', taskId: 't-1' }))
    expect(items[0].status).toBe('cancelled')
  })

  it('迟到事件会补建一行而不是丢失结果', () => {
    let items = seeded()
    items = applyProgress(
      items,
      event({ kind: 'done', taskId: 'late-9', file: 'C:\\Pics\\c.png', index: 5, output: 'C:\\Pics\\c_chronos_x4.png' }),
    )
    expect(items).toHaveLength(3)
    const created = items[items.length - 1]
    expect(created.taskId).toBe('late-9')
    expect(created.status).toBe('done')
    expect(created.output).toBe('C:\\Pics\\c_chronos_x4.png')
  })

  it('done 事件不会让 done 超过 total', () => {
    let items = seeded()
    items = applyProgress(items, event({ kind: 'progress', taskId: 't-1', done: 50, total: 10 }))
    expect(items[0].done).toBe(10)
  })
})

describe('summarize / removeItem / clearFinished', () => {
  it('按状态汇总并计算完成比例', () => {
    let items = enqueue([], ['C:\\a.jpg', 'C:\\b.jpg', 'C:\\c.jpg']).items
    items = applyProgress(items, event({ kind: 'done', taskId: 't-a', file: 'C:\\a.jpg' }))
    items = applyProgress(items, event({ kind: 'failed', taskId: 't-b', file: 'C:\\b.jpg', error: 'boom' }))
    const summary = summarize(items)
    expect(summary.total).toBe(3)
    expect(summary.done).toBe(1)
    expect(summary.failed).toBe(1)
    expect(summary.queued).toBe(1)
    expect(summary.progress).toBeCloseTo(1 / 3, 5)
  })

  it('removeItem 支持 id 与 taskId；clearFinished 只保留排队/进行中', () => {
    let items = enqueue([], ['C:\\a.jpg', 'C:\\b.jpg', 'C:\\c.jpg']).items
    items = applyProgress(items, event({ kind: 'done', taskId: 't-a', file: 'C:\\a.jpg' }))
    items = applyProgress(items, event({ kind: 'started', taskId: 't-b', file: 'C:\\b.jpg' }))
    expect(removeItem(items, 'C:\\c.jpg')).toHaveLength(2)
    expect(removeItem(items, 't-a')).toHaveLength(2)
    const cleared = clearFinished(items)
    expect(cleared).toHaveLength(2)
    expect(cleared.map((item) => item.status)).toEqual(['running', 'queued'])
  })
})
