/**
 * 浏览器预览用的 mock 后端（契约 §5：非桌面环境自动进入 mock）。
 *
 * 目标：`vite dev` 打开就能完整走一遍「选文件 → 调参 → 开始 → tile 进度 → 对比结果
 * → 阻止睡眠状态 → 日志」的链路，全部事件与字段形态和真实 Wails 事件一致，
 * 绝不因为缺少 window.go 而崩溃。
 */
import type {
  AccelStatus,
  AppInfo,
  BatchRequest,
  BatchResult,
  DirEntry,
  DirListing,
  InhibitRequest,
  InhibitStatus,
  JobSummary,
  ModelDiagnostics,
  ModelInfo,
  ModelPullState,
  ModelSelfTest,
  ProgressEvent,
} from './types'
import { fileLabel } from './queue'
import { mockEmit } from './eventbus'
import type { Api } from './api'

/* ------------------------------------------------------------------ 事件总线 */

type MockJob = {
  id: string
  total: number
  startedAt: number
  cancelled: boolean
  timer: ReturnType<typeof setTimeout> | null
  files: string[]
  completed: number
  failed: number
  autoRelease: boolean
}

const SAMPLE_MODELS: ModelInfo[] = [
  {
    id: 'photo-realesrgan-x4',
    name: 'Real-ESRGAN x4plus',
    scale: 4,
    kind: 'photo',
    description: '真实照片通用修复，纹理自然，适合人像与风景。',
    sizeMb: 63.9,
    installed: true,
    path: 'C:\\Users\\Public\\Chronos\\models\\realesrgan-x4plus.onnx',
    url: 'https://github.com/xinntao/Real-ESRGAN/releases',
  },
  {
    id: 'photo-realesrgan-x2',
    name: 'Real-ESRGAN x2plus',
    scale: 2,
    kind: 'photo',
    description: '2 倍轻量版本，速度约为 x4 的两倍。',
    sizeMb: 63.9,
    installed: true,
    path: 'C:\\Users\\Public\\Chronos\\models\\realesrgan-x2plus.onnx',
    url: 'https://github.com/xinntao/Real-ESRGAN/releases',
  },
  {
    id: 'anime-waifu2x-x4',
    name: 'Anime waifu2x x4',
    scale: 4,
    kind: 'anime',
    description: '动漫线稿专用，抑制振铃并保留锐利边缘。',
    sizeMb: 8.1,
    installed: false,
    path: '',
    url: 'https://github.com/nagadomi/nunif/releases',
  },
  {
    id: 'compressed-jpeg-x4',
    name: 'JPEG Deblock x4',
    scale: 4,
    kind: 'compressed',
    description: '针对低质量 JPEG 的块效应与蚊噪抑制。',
    sizeMb: 16.4,
    installed: false,
    path: '',
    url: 'https://github.com/Colab-Community/JPEG-Deblock',
  },
  {
    id: 'general-lanczos',
    name: '通用 Lanczos x3',
    scale: 3,
    kind: 'general',
    description: '无模型回退路径（stub 后端），任意倍率稳定输出。',
    sizeMb: 0,
    installed: true,
    path: '',
    url: '',
  },
]

const MOCK_PICTURES = ['C:\\Users\\Public\\Pictures']

/* ------------------------------------------- 模拟文件系统（PathPicker 浏览器预览用） */

const MOCK_HOME: string = MOCK_PICTURES[0]
const MOCK_SCENERY = `${MOCK_HOME}\\风景`
const MOCK_PORTRAIT = `${MOCK_HOME}\\人像`
const MOCK_CITY = `${MOCK_SCENERY}\\城市`

function mockDir(name: string, path: string): DirEntry {
  return { name, path, isDir: true, size: 0, isImage: false }
}

function mockImage(dir: string, name: string, size: number): DirEntry {
  return { name, path: `${dir}\\${name}`, isDir: false, size, isImage: true }
}

const MOCK_TREE: Record<string, DirEntry[]> = {
  [MOCK_HOME]: [
    mockDir('风景', MOCK_SCENERY),
    mockDir('人像', MOCK_PORTRAIT),
    mockImage(MOCK_HOME, 'chronos-sample-01.jpg', 2_411_520),
    mockImage(MOCK_HOME, 'chronos-sample-02.jpg', 3_145_728),
    mockImage(MOCK_HOME, 'chronos-sample-03.png', 5_242_880),
    { name: 'notes.txt', path: `${MOCK_HOME}\\notes.txt`, isDir: false, size: 1024, isImage: false },
  ],
  [MOCK_SCENERY]: [
    mockDir('城市', MOCK_CITY),
    mockImage(MOCK_SCENERY, 'landscape-01.jpg', 4_194_304),
    mockImage(MOCK_SCENERY, 'landscape-02.jpg', 5_033_164),
    mockImage(MOCK_SCENERY, 'landscape-03.webp', 1_572_864),
  ],
  [MOCK_CITY]: [
    mockImage(MOCK_CITY, 'street-01.jpg', 3_355_443),
    mockImage(MOCK_CITY, 'street-02.png', 4_718_592),
  ],
  [MOCK_PORTRAIT]: [
    mockImage(MOCK_PORTRAIT, 'portrait-01.jpg', 2_097_152),
    mockImage(MOCK_PORTRAIT, 'portrait-02.jpg', 2_621_440),
  ],
}

/** Windows 风格的取父目录（盘符根返回空串，表示已到顶）。 */
function dirnameOf(path: string): string {
  const trimmed = path.replace(/[\\/]+$/, '')
  if (!trimmed) return ''
  const idx = Math.max(trimmed.lastIndexOf('\\'), trimmed.lastIndexOf('/'))
  if (idx < 0) return ''
  if (idx === 2 && /^[A-Za-z]:[\\/]/.test(trimmed)) return trimmed.slice(0, 3)
  return trimmed.slice(0, idx)
}

/** 与 Go 侧语义一致：空入参 = home 目录；未知目录返回空列表但仍给出 parent。 */
function mockListDirectory(path: string): DirListing {
  const target = (path ?? '').trim() || MOCK_HOME
  const entries = MOCK_TREE[target] ?? []
  return {
    path: target,
    parent: dirnameOf(target),
    entries: entries.map((entry) => ({ ...entry })),
  }
}

const state = {
  concurrency: 4,
  inhibited: {
    sleep: false,
    display: false,
    lock: false,
  },
  autoRelease: true,
  sinceMs: 0,
  job: null as MockJob | null,
  jobSeq: 0,
  tickMs: 140,
}

function now(): number {
  return Date.now()
}

function appInfo(): AppInfo {
  return {
    version: '0.1.0',
    commit: 'preview',
    buildTime: new Date(0).toISOString(),
    goVersion: 'n/a (browser mock)',
    platform: 'browser/preview',
    engine: {
      backend: 'stub',
      device: 'CPU',
      detail: '浏览器预览：本地 stub 模拟，不会真正推理',
      abiVersion: 1,
    },
    dataDir: 'C:\\Users\\Public\\Chronos',
    modelDir: 'C:\\Users\\Public\\Chronos\\models',
    outputDir: 'C:\\Users\\Public\\Chronos\\output',
    maxConcurrency: 8,
    nativeLoaded: false,
    mode: 'desktop',
    browseSupported: true,
  }
}

function inhibitStatus(): InhibitStatus {
  const anyActive = state.inhibited.sleep || state.inhibited.display || state.inhibited.lock
  return {
    active: anyActive,
    sleep: state.inhibited.sleep,
    display: state.inhibited.display,
    lock: state.inhibited.lock,
    sleepSupported: true,
    displaySupported: true,
    lockSupported: true,
    driver: 'browser-mock',
    detail: '浏览器预览无法真正阻止系统睡眠；桌面端由 Go 侧调用系统 API。',
    sinceMs: anyActive ? state.sinceMs || now() : 0,
  }
}

function emitInhibit(): void {
  mockEmit('chronos:inhibit', inhibitStatus())
}

function emitLog(level: string, msg: string): void {
  mockEmit('chronos:log', { level, msg, timeMs: now() })
}

function clearJobTimer(job: MockJob | null): void {
  if (job?.timer) {
    clearTimeout(job.timer)
    job.timer = null
  }
}

function hash(input: string): number {
  let h = 2166136261
  for (let i = 0; i < input.length; i += 1) {
    h ^= input.charCodeAt(i)
    h = Math.imul(h, 16777619)
  }
  return h >>> 0
}

/** 文件名里出现这些词时模拟失败，便于在浏览器里预览失败态。 */
const FAIL_HINTS = ['corrupt', 'broken', 'fail']

function willFail(file: string): boolean {
  const lower = file.toLowerCase()
  return FAIL_HINTS.some((hint) => lower.includes(hint))
}

function outputPathFor(req: BatchRequest, file: string): string {
  const slash = Math.max(file.lastIndexOf('\\'), file.lastIndexOf('/'))
  const dir = req.outputDir || (slash >= 0 ? file.slice(0, slash) : '')
  const base = fileLabel(file).replace(/\.[^.\\/]+$/, '')
  return `${dir}\\${base}_chronos_x${req.scale || 1}.png`
}

/** 推进一个模拟任务：按文件串行、按 tile 出进度事件。 */
function tickJob(job: MockJob): void {
  const file = job.files[job.completed + job.failed]
  if (!file) {
    finishJob(job)
    return
  }
  const index = job.completed + job.failed
  const taskId = `task-${job.id}-${index}`
  const totalTiles = 6 + (hash(file) % 18)
  let done = 0
  const startedAt = now()
  const fail = willFail(file)
  const failAt = Math.max(2, Math.floor(totalTiles * 0.45))
  const baseElapsed = now() - job.startedAt

  const send = (kind: string, extra: Partial<ProgressEvent> = {}) => {
    const event: ProgressEvent = {
      jobId: job.id,
      taskId,
      file,
      kind,
      index,
      done,
      total: totalTiles,
      output: '',
      width: 0,
      height: 0,
      error: '',
      backend: 'stub',
      elapsedMs: now() - startedAt,
      ...extra,
    }
    mockEmit('chronos:progress', event)
  }

  mockEmit('chronos:progress', {
    jobId: job.id,
    taskId,
    file,
    kind: 'queued',
    index,
    done: 0,
    total: totalTiles,
    output: '',
    width: 0,
    height: 0,
    error: '',
    backend: 'stub',
    elapsedMs: baseElapsed,
  })
  send('started')

  const step = () => {
    if (job.cancelled) {
      send('cancelled')
      return
    }
    done += 1
    if (fail && done >= failAt) {
      job.failed += 1
      send('failed', { error: '模拟失败：解码阶段遇到损坏数据 (CHRONOS_ERR_IO)' })
      job.timer = setTimeout(() => tickJob(job), Math.max(1, Math.round(state.tickMs / 2)))
      return
    }
    if (done >= totalTiles) {
      job.completed += 1
      const width = 1200 + (hash(file) % 7) * 64
      const height = 800 + (hash(file) % 5) * 48
      send('done', {
        done: totalTiles,
        total: totalTiles,
        output: outputPathFor(jobRequest, file),
        width: width * (jobRequest.scale || 1),
        height: height * (jobRequest.scale || 1),
      })
      mockEmit('chronos:log', {
        level: 'info',
        msg: `完成 ${fileLabel(file)} → ${fileLabel(outputPathFor(jobRequest, file))}`,
        timeMs: now(),
      })
      job.timer = setTimeout(() => tickJob(job), Math.max(1, Math.round(state.tickMs / 2)))
      return
    }
    send('progress')
    job.timer = setTimeout(step, state.tickMs)
  }

  job.timer = setTimeout(step, Math.max(1, Math.round(state.tickMs / 2)))
}

function finishJob(job: MockJob): void {
  const summary: JobSummary = {
    jobId: job.id,
    total: job.total,
    completed: job.completed,
    failed: job.failed,
    cancelled: job.cancelled,
    elapsedMs: now() - job.startedAt,
  }
  mockEmit('chronos:job', summary)
  mockEmit('chronos:log', {
    level: job.cancelled ? 'warn' : job.failed > 0 ? 'warn' : 'info',
    msg: job.cancelled
      ? `任务已取消：完成 ${job.completed} / 失败 ${job.failed} / 共 ${job.total}`
      : `任务结束：完成 ${job.completed} / 失败 ${job.failed} / 共 ${job.total}`,
    timeMs: now(),
  })
  if (job.autoRelease) {
    state.inhibited.sleep = false
    state.inhibited.display = false
    state.inhibited.lock = false
    state.sinceMs = 0
    emitInhibit()
    emitLog('info', '任务结束，已自动释放电源保持')
  }
  if (state.job === job) state.job = null
}

function startJob(req: BatchRequest): BatchResult {
  const files = (req.files ?? []).filter((file) => typeof file === 'string' && file.trim() !== '')
  const skipped: string[] = []
  const unique: string[] = []
  const seen = new Set<string>()
  for (const file of files) {
    const key = file.toLowerCase()
    if (seen.has(key)) skipped.push(file)
    else {
      seen.add(key)
      unique.push(file)
    }
  }
  if (unique.length === 0) throw new Error('没有可处理的文件')

  clearJobTimer(state.job)
  state.jobSeq += 1
  const job: MockJob = {
    id: `mock-job-${state.jobSeq}`,
    total: unique.length,
    startedAt: now(),
    cancelled: false,
    timer: null,
    files: unique,
    completed: 0,
    failed: 0,
    autoRelease: !!req.inhibit?.autoRelease,
  }
  state.job = job
  jobRequest = req

  if (req.inhibit && (req.inhibit.sleep || req.inhibit.display || req.inhibit.lock)) {
    state.inhibited.sleep = !!req.inhibit.sleep
    state.inhibited.display = !!req.inhibit.display
    state.inhibited.lock = !!req.inhibit.lock
    state.sinceMs = now()
    emitInhibit()
  }

  emitLog(
    'info',
    `模拟任务 ${job.id} 启动：${unique.length} 个文件，并发 ${req.concurrency}，tile ${req.tileSize} / overlap ${req.overlap}，scale x${req.scale}`,
  )
  job.timer = setTimeout(() => tickJob(job), Math.max(1, Math.round(state.tickMs / 2)))
  return { jobId: job.id, queued: unique.length, skipped }
}

/** 最近一次 BatchRequest（mock 用于拼输出路径与倍率）。 */
let jobRequest: BatchRequest = {
  files: [],
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

/* -------------------------------------------------------------------- Api 实现 */

export const mockApi: Api = {
  async GetAppInfo() {
    return appInfo()
  },
  async ListModels() {
    return SAMPLE_MODELS.map((model) => ({ ...model }))
  },
  async RefreshModels() {
    // 模拟扫描目录：把没有本地路径的模型标记为可下载但未安装
    return SAMPLE_MODELS.map((model) => ({ ...model }))
  },
  async UpscaleBatch(req) {
    return startJob(req)
  },
  async CancelJob(jobId) {
    const job = state.job
    if (!job || job.id !== jobId) return false
    job.cancelled = true
    clearJobTimer(job)
    const file = job.files[job.completed + job.failed]
    if (file) {
      mockEmit('chronos:progress', {
        jobId: job.id,
        taskId: `task-${job.id}-${job.completed + job.failed}`,
        file,
        kind: 'cancelled',
        index: job.completed + job.failed,
        done: 0,
        total: 0,
        output: '',
        width: 0,
        height: 0,
        error: '用户取消',
        backend: 'stub',
        elapsedMs: now() - job.startedAt,
      } satisfies ProgressEvent)
    }
    finishJob(job)
    return true
  },
  async SetConcurrency(n) {
    if (!Number.isInteger(n) || n < 1 || n > 8) throw new Error('并发数必须在 1~8 之间')
    state.concurrency = n
    emitLog('info', `并发数已设为 ${n}`)
  },
  async GetConcurrency() {
    return state.concurrency
  },
  async SetInhibit(req: InhibitRequest) {
    state.inhibited.sleep = !!req.sleep
    state.inhibited.display = !!req.display
    state.inhibited.lock = !!req.lock
    state.autoRelease = req.autoRelease !== false
    const active = state.inhibited.sleep || state.inhibited.display || state.inhibited.lock
    if (active && state.sinceMs === 0) state.sinceMs = now()
    if (!active) state.sinceMs = 0
    emitInhibit()
    emitLog(
      'info',
      active
        ? `保持唤醒：睡眠=${state.inhibited.sleep ? '开' : '关'} 熄屏=${state.inhibited.display ? '开' : '关'} 锁屏=${state.inhibited.lock ? '开' : '关'}`
        : '已释放电源保持',
    )
    return inhibitStatus()
  },
  async GetInhibitStatus() {
    return inhibitStatus()
  },
  async PickImages() {
    emitLog('debug', '（浏览器预览）返回示例图片路径')
    return [
      `${MOCK_PICTURES[0]}\\chronos-sample-01.jpg`,
      `${MOCK_PICTURES[0]}\\chronos-sample-02.jpg`,
      `${MOCK_PICTURES[0]}\\chronos-sample-03.png`,
    ]
  },
  async PickOutputDir() {
    emitLog('debug', '（浏览器预览）返回示例输出目录')
    return 'C:\\Users\\Public\\Chronos\\output'
  },
  async Reveal(path) {
    emitLog('debug', `（浏览器预览）在资源管理器中定位：${path}`)
  },
  async OpenDataDir() {
    emitLog('debug', '（浏览器预览）打开数据目录：C:\\Users\\Public\\Chronos')
  },
  async ListDirectory(path) {
    const listing = mockListDirectory(path)
    emitLog('debug', `（浏览器预览）枚举目录：${listing.path}（${listing.entries.length} 项）`)
    return listing
  },
  async PickModelFile() {
    emitLog('debug', '（浏览器预览）返回示例模型路径')
    return 'C:\\Users\\Public\\Chronos\\models\\sample.onnx'
  },
  async PullModel(req): Promise<ModelPullState> {
    const model = SAMPLE_MODELS.find((m) => m.id === req.modelId)
    const total = Math.round((model?.sizeMb || 8) * 1024 * 1024)
    let done = 0
    const step = Math.max(1, Math.round(total / 8))
    const timer = setInterval(() => {
      done = Math.min(total, done + step)
      const phase = done >= total ? 'done' : 'downloading'
      mockEmit('chronos:model', { modelId: req.modelId, phase, done, total, error: '' })
      if (phase === 'done') {
        clearInterval(timer)
        pullTimers.delete(req.modelId)
        const m = SAMPLE_MODELS.find((x) => x.id === req.modelId)
        if (m) {
          m.installed = true
          m.verified = true
        }
      }
    }, Math.max(1, Math.round(state.tickMs)))
    pullTimers.set(req.modelId, timer)
    const initial: ModelPullState = { modelId: req.modelId, phase: 'queued', done: 0, total, error: '' }
    mockEmit('chronos:model', initial)
    return initial
  },
  async CancelPull(modelId) {
    const timer = pullTimers.get(modelId)
    if (!timer) return false
    clearInterval(timer)
    pullTimers.delete(modelId)
    mockEmit('chronos:model', { modelId, phase: 'cancelled', done: 0, total: 0, error: '' })
    return true
  },
  async RemoveModel(modelId) {
    const model = SAMPLE_MODELS.find((m) => m.id === modelId)
    if (model) {
      model.installed = false
      model.verified = false
      model.failReason = ''
    }
    emitLog('info', `（浏览器预览）已删除模型 ${modelId}`)
  },
  async ImportModel(req) {
    const imported: ModelInfo = {
      id: `imported-${SAMPLE_MODELS.length + 1}`,
      name: req.name || '导入模型',
      scale: 4,
      kind: 'photo',
      description: req.path,
      sizeMb: 12,
      installed: true,
      path: req.path,
      url: '',
      origin: 'imported',
      verified: false,
      license: '',
      arch: '',
      sidecar: true,
      failReason: '',
    }
    SAMPLE_MODELS.push(imported)
    emitLog('info', `（浏览器预览）已导入模型：${imported.name}`)
    return { ...imported }
  },
  async SelfTestModel(modelId): Promise<ModelSelfTest> {
    const model = SAMPLE_MODELS.find((m) => m.id === modelId)
    if (model) model.verified = true
    return {
      ok: true,
      checks: [
        { name: 'load', ok: true, detail: '（模拟）后端 ort · CPU · ort/CPU' },
        { name: 'size', ok: true, detail: '（模拟）输出 256x256，期望 256x256' },
        { name: 'color', ok: true, detail: '（模拟）均值 96.4，标准差 52.31' },
        { name: 'seam', ok: true, detail: '（模拟）tile 256 vs 512：最大像素差 1 LSB' },
        { name: 'perf', ok: true, detail: '（模拟）412 ms/图' },
      ],
      diagnostics: `mock self test for ${modelId}`,
    }
  },
  async GetModelDiagnostics(modelId): Promise<ModelDiagnostics> {
    return {
      modelId,
      opset: 0,
      ops: [],
      inputs: [],
      outputs: [],
      warnings: ['（浏览器预览）诊断数据为模拟值，真实解析在 P4 提供'],
      error: '',
    }
  },
  async GetAccelStatus(): Promise<AccelStatus> {
    return mockAccelStatus()
  },
  async SetAccelMode(mode) {
    mockAccelMode = mode === 'cpu' ? 'cpu' : 'auto'
    emitLog('info', `（浏览器预览）加速方式：${mockAccelMode}`)
    return mockAccelStatus()
  },
  async InstallAccelRuntime() {
    mockGpuInstalled = true
    emitLog('info', '（浏览器预览）已模拟安装 GPU 运行时')
    return mockAccelStatus()
  },
}

/** 浏览器预览的加速状态：永远是 CPU（没有真实运行时）。 */
let mockAccelMode = 'auto'
let mockGpuInstalled = false
function mockAccelStatus(): AccelStatus {
  const info = appInfo().engine
  return {
    mode: mockAccelMode,
    active: info,
    available: mockGpuInstalled ? ['CPU', 'DML'] : ['CPU'],
    gpuUsable: false,
    fallbackReason: mockGpuInstalled
      ? '（浏览器预览）已安装 GPU 运行时，但预览模式不会真正推理'
      : '（浏览器预览）没有可用的 GPU 执行后端',
    gpuRuntime: {
      flavor: 'directml',
      installed: mockGpuInstalled,
      dir: 'C:\\Users\\Public\\Chronos\\runtime\\ort\\directml-1.24.4',
      installable: false,
      version: '1.24.4',
      sizeMb: 11.9,
    },
    installPhase: mockGpuInstalled ? 'done' : '',
    installDone: 0,
    installTotal: 0,
    installError: '',
    restartRequired: false,
  }
}

/** 进行中的模拟下载（modelId -> timer）。 */
const pullTimers = new Map<string, ReturnType<typeof setInterval>>()

/* ------------------------------------------------------------- 测试辅助（导出） */

/** 调整模拟速度（测试里设成 1ms 让任务瞬间跑完）。 */
export function configureMock(options: { tickMs?: number } = {}): void {
  if (typeof options.tickMs === 'number' && options.tickMs > 0) state.tickMs = options.tickMs
}

/** 复位 mock 状态，避免测试之间互相污染。 */
export function resetMock(): void {
  clearJobTimer(state.job)
  state.job = null
  state.jobSeq = 0
  state.concurrency = 4
  state.inhibited = { sleep: false, display: false, lock: false }
  state.autoRelease = true
  state.sinceMs = 0
  state.tickMs = 140
}
