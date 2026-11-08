/**
 * Chronos UI 的状态中枢：队列 / 任务生命周期 / 事件订阅 / 并发同步 / 阻止睡眠。
 *
 * 所有跨层数据都从 api.ts 进入；这里只做状态归并与展示派生，
 * 队列本身的可测逻辑放在 lib/queue.ts（纯函数，见 __tests__）。
 */
import { computed, onScopeDispose, reactive, ref } from 'vue'
import { api, isDesktop, isMockMode, onEvent } from '@/lib/api'
import {
  applyProgress,
  clearFinished as clearFinishedItems,
  enqueue,
  itemPercent,
  pendingCount,
  removeItem as removeQueueItem,
  summarize,
  type QueueItem,
} from '@/lib/queue'
import type {
  AppInfo,
  BatchRequest,
  InhibitRequest,
  InhibitStatus,
  JobSummary,
  LogEvent,
  ModelInfo,
  ProgressEvent,
} from '@/lib/types'

const MAX_LOG_ENTRIES = 500
const MAX_RECENT_RESULTS = 12
const TOAST_TTL_MS = 4200

export type ToastKind = 'info' | 'success' | 'warn' | 'error'

export interface Toast {
  id: number
  kind: ToastKind
  message: string
}

export interface RecentResult {
  path: string
  source: string
  name: string
  width: number
  height: number
  scale: number
  at: number
}

export interface CompareTarget {
  file: string
  output: string
  name: string
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value))
}

function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  if (typeof error === 'string') return error
  if (error && typeof error === 'object' && 'message' in error) {
    return String((error as { message: unknown }).message)
  }
  return '未知错误'
}

function emptyInhibit(): InhibitStatus {
  return {
    active: false,
    sleep: false,
    display: false,
    lock: false,
    sleepSupported: false,
    displaySupported: false,
    lockSupported: false,
    driver: 'unsupported',
    detail: '',
    sinceMs: 0,
  }
}

export function useChronos() {
  /* ------------------------------------------------------------- 基础状态 */
  const appInfo = ref<AppInfo | null>(null)
  const models = ref<ModelInfo[]>([])
  const modelsLoading = ref(false)
  const loading = ref(false)
  const initError = ref('')

  /* ------------------------------------------------------------- 参数状态 */
  const selectedModelId = ref('')
  const scale = ref(4)
  const outputDir = ref('')
  const concurrency = ref(4)
  const tileSize = ref(256)
  const overlap = ref(16)
  const threads = ref(0)
  const fast = ref(false)
  const inhibitRequest = reactive<InhibitRequest>({
    sleep: false,
    display: false,
    lock: false,
    autoRelease: true,
  })

  /* ------------------------------------------------------------- 运行状态 */
  const items = ref<QueueItem[]>([])
  const logs = ref<LogEvent[]>([])
  const toasts = ref<Toast[]>([])
  const inhibitStatus = ref<InhibitStatus>(emptyInhibit())
  const job = ref<JobSummary | null>(null)
  const runningJobId = ref('')
  const starting = ref(false)
  const recent = ref<RecentResult[]>([])
  const compareTarget = ref<CompareTarget | null>(null)

  let toastSeq = 0
  let disposed = false
  const unsubscribers: Array<() => void> = []
  const toastTimers = new Map<number, ReturnType<typeof setTimeout>>()

  /* --------------------------------------------------------------- 派生值 */
  const selectedModel = computed<ModelInfo | null>(
    () => models.value.find((model) => model.id === selectedModelId.value) ?? null,
  )
  /** 有模型时倍率以模型原生 scale 为准（契约 §1 stub 支持 1..4；模型以其原生值为准）。 */
  const effectiveScale = computed(() => {
    const model = selectedModel.value
    return model && model.scale > 0 ? model.scale : scale.value
  })
  const maxConcurrency = computed(() => clamp(appInfo.value?.maxConcurrency ?? 8, 1, 8))
  const queueSummary = computed(() => summarize(items.value))
  const pending = computed(() => pendingCount(items.value))
  const running = computed(() => runningJobId.value !== '' || queueSummary.value.running > 0)
  const canStart = computed(() => !starting.value && !running.value && pending.value > 0)
  const engineLabel = computed(() => {
    const engine = appInfo.value?.engine
    if (!engine) return '引擎未就绪'
    return `${engine.backend}${engine.device && engine.device !== 'n/a' ? ` · ${engine.device}` : ''}`
  })
  const anyInhibit = computed(
    () => inhibitRequest.sleep || inhibitRequest.display || inhibitRequest.lock,
  )
  const isDesktopMode = isDesktop()

  /* --------------------------------------------------------------- 日志/提示 */
  function pushLog(level: LogEvent['level'], msg: string): void {
    const entry: LogEvent = {
      level,
      msg,
      timeMs: Date.now(),
    }
    const next = logs.value.concat(entry)
    logs.value = next.length > MAX_LOG_ENTRIES ? next.slice(next.length - MAX_LOG_ENTRIES) : next
  }

  function dismissToast(id: number): void {
    const timer = toastTimers.get(id)
    if (timer) clearTimeout(timer)
    toastTimers.delete(id)
    toasts.value = toasts.value.filter((toast) => toast.id !== id)
  }

  function toast(kind: ToastKind, message: string): void {
    const entry: Toast = { id: ++toastSeq, kind, message }
    toasts.value = toasts.value.concat(entry).slice(-4)
    const timer = setTimeout(() => dismissToast(entry.id), TOAST_TTL_MS)
    toastTimers.set(entry.id, timer)
    if (kind === 'error' || kind === 'warn') pushLog(kind, message)
  }

  /* --------------------------------------------------------------- 数据加载 */
  async function loadModels(refresh = false): Promise<void> {
    modelsLoading.value = true
    try {
      const list = refresh ? await api.RefreshModels() : await api.ListModels()
      models.value = Array.isArray(list) ? list : []
      // 保持已选模型；若失效则回退到第一个已安装模型，或空（= stub 引擎）
      const stillThere = models.value.some((model) => model.id === selectedModelId.value)
      if (!stillThere) {
        const fallback = models.value.find((model) => model.installed) ?? null
        selectedModelId.value = fallback?.id ?? ''
      }
      pushLog('info', `模型列表已更新：${models.value.length} 个，已安装 ${models.value.filter((m) => m.installed).length} 个`)
    } catch (error) {
      const message = errorMessage(error)
      toast('error', `读取模型列表失败：${message}`)
    } finally {
      modelsLoading.value = false
    }
  }

  async function refreshInhibit(): Promise<void> {
    try {
      inhibitStatus.value = await api.GetInhibitStatus()
      if (!disposed) {
        inhibitRequest.sleep = inhibitStatus.value.sleep
        inhibitRequest.display = inhibitStatus.value.display
        inhibitRequest.lock = inhibitStatus.value.lock
      }
    } catch (error) {
      pushLog('warn', `读取电源保持状态失败：${errorMessage(error)}`)
    }
  }

  async function init(): Promise<void> {
    if (loading.value || disposed) return
    loading.value = true
    initError.value = ''
    pushLog('info', isDesktopMode ? 'Chronos 桌面运行时已连接' : '浏览器预览模式：使用 mock 后端')
    subscribeEvents()
    try {
      const [info, concurrencyValue] = await Promise.all([
        api.GetAppInfo(),
        api.GetConcurrency().catch(() => 4),
      ])
      appInfo.value = info
      outputDir.value = outputDir.value || info?.outputDir || ''
      concurrency.value = clamp(Number(concurrencyValue) || 4, 1, 8)
      await Promise.all([loadModels(), refreshInhibit()])
      pushLog(
        'info',
        `引擎 ${info?.engine?.backend ?? '?'} / ${info?.engine?.device ?? '?'}，并发上限 ${info?.maxConcurrency ?? 8}`,
      )
    } catch (error) {
      initError.value = errorMessage(error)
      toast('error', `初始化失败：${initError.value}`)
    } finally {
      loading.value = false
    }
  }

  /* --------------------------------------------------------------- 事件订阅 */
  function handleProgress(event: ProgressEvent): void {
    items.value = applyProgress(items.value, event)
    if (event.kind === 'done' && event.output) {
      const scaleUsed = effectiveScale.value
      addRecent({
        path: event.output,
        source: event.file,
        name: event.file,
        width: event.width,
        height: event.height,
        scale: scaleUsed,
        at: Date.now(),
      })
    } else if (event.kind === 'failed') {
      pushLog('error', `处理失败：${event.file || event.taskId} — ${event.error || '未知错误'}`)
    }
  }

  function handleJob(summary: JobSummary): void {
    job.value = summary
    runningJobId.value = ''
    starting.value = false
    if (summary.cancelled) {
      // 还没轮到处理的排队项统一切成已取消，避免按钮永远可点
      items.value = items.value.map((item) =>
        item.status === 'queued' || item.status === 'running'
          ? { ...item, status: 'cancelled', error: item.error || '任务已取消' }
          : item,
      )
    }
    const label = summary.cancelled
      ? `任务已取消（完成 ${summary.completed} / 失败 ${summary.failed} / 共 ${summary.total}）`
      : `任务完成（完成 ${summary.completed} / 失败 ${summary.failed} / 共 ${summary.total}）`
    toast(summary.failed > 0 || summary.cancelled ? 'warn' : 'success', label)
    void refreshInhibit()
  }

  function subscribeEvents(): void {
    unsubscribers.push(onEvent('chronos:progress', handleProgress))
    unsubscribers.push(onEvent('chronos:job', handleJob))
    unsubscribers.push(onEvent('chronos:log', (entry) => {
      const next = logs.value.concat(entry)
      logs.value = next.length > MAX_LOG_ENTRIES ? next.slice(next.length - MAX_LOG_ENTRIES) : next
    }))
    unsubscribers.push(onEvent('chronos:inhibit', (status) => {
      inhibitStatus.value = status
      if (!disposed) {
        inhibitRequest.sleep = status.sleep
        inhibitRequest.display = status.display
        inhibitRequest.lock = status.lock
      }
    }))
  }

  function dispose(): void {
    if (disposed) return
    disposed = true
    for (const unsubscribe of unsubscribers.splice(0)) {
      try {
        unsubscribe()
      } catch {
        // 忽略退订异常（窗口正在销毁时 Wails 可能已清理事件表）
      }
    }
    for (const timer of toastTimers.values()) clearTimeout(timer)
    toastTimers.clear()
  }

  onScopeDispose(dispose)

  /* ------------------------------------------------------------- 队列操作 */
  function addFiles(paths: readonly string[]): void {
    if (!paths.length) return
    const result = enqueue(items.value, paths)
    items.value = result.items
    if (result.added.length > 0) {
      pushLog('info', `已加入队列：${result.added.map((item) => item.name).join('、')}`)
    }
    if (result.duplicates.length > 0) {
      toast('warn', `已跳过 ${result.duplicates.length} 个重复文件`)
    }
  }

  async function pickImages(): Promise<void> {
    try {
      const paths = await api.PickImages()
      if (Array.isArray(paths) && paths.length > 0) addFiles(paths)
      else if (paths && paths.length === 0) pushLog('debug', '未选择任何文件')
    } catch (error) {
      toast('error', `选择图片失败：${errorMessage(error)}`)
    }
  }

  async function pickOutputDir(): Promise<void> {
    try {
      const dir = await api.PickOutputDir()
      if (dir) {
        outputDir.value = dir
        pushLog('info', `输出目录：${dir}`)
      }
    } catch (error) {
      toast('error', `选择输出目录失败：${errorMessage(error)}`)
    }
  }

  function removeItem(id: string): void {
    items.value = removeQueueItem(items.value, id)
  }

  function clearFinished(): void {
    const before = items.value.length
    items.value = clearFinishedItems(items.value)
    const removed = before - items.value.length
    if (removed > 0) pushLog('debug', `已清理 ${removed} 条已结束记录`)
  }

  /* --------------------------------------------------------------- 参数同步 */
  async function setConcurrency(value: number): Promise<void> {
    const next = clamp(Math.round(value), 1, maxConcurrency.value)
    const previous = concurrency.value
    concurrency.value = next
    if (next === previous) return
    try {
      await api.SetConcurrency(next)
    } catch (error) {
      concurrency.value = previous
      toast('error', errorMessage(error))
    }
  }

  async function updateInhibit(patch: Partial<InhibitRequest>): Promise<void> {
    Object.assign(inhibitRequest, patch)
    try {
      const status = await api.SetInhibit({ ...inhibitRequest })
      inhibitStatus.value = status
    } catch (error) {
      toast('error', `设置电源保持失败：${errorMessage(error)}`)
      await refreshInhibit()
    }
  }

  async function reveal(path: string): Promise<void> {
    try {
      await api.Reveal(path)
    } catch (error) {
      toast('warn', `无法定位文件：${errorMessage(error)}`)
    }
  }

  async function openDataDir(): Promise<void> {
    try {
      await api.OpenDataDir()
    } catch (error) {
      toast('warn', `无法打开数据目录：${errorMessage(error)}`)
    }
  }

  /* --------------------------------------------------------------- 任务控制 */
  function buildRequest(files: string[]): BatchRequest {
    const model = selectedModel.value
    const useModel = !!model && model.installed && model.path !== ''
    return {
      files,
      modelId: useModel ? model!.id : '',
      modelPath: useModel ? model!.path : '',
      scale: effectiveScale.value,
      outputDir: outputDir.value,
      concurrency: concurrency.value,
      tileSize: tileSize.value,
      overlap: overlap.value,
      threads: threads.value,
      fast: fast.value,
      inhibit: anyInhibit.value ? { ...inhibitRequest } : null,
    }
  }

  function validateRequest(request: BatchRequest, queued: number): string {
    if (queued === 0) return '队列中没有待处理的文件'
    if (request.scale < 1 || request.scale > 8) return '倍率必须在 1~8 之间'
    if (!Number.isFinite(request.tileSize) || request.tileSize < 32 || request.tileSize > 1024) {
      return 'tile 尺寸必须在 32~1024 之间'
    }
    if (request.overlap < 0 || request.overlap > 128) return 'overlap 必须在 0~128 之间'
    if (request.overlap >= request.tileSize) return 'overlap 必须小于 tile 尺寸'
    if (request.threads < 0 || request.threads > 256) return '线程数必须在 0~256 之间（0 = 自动）'
    if (request.concurrency < 1 || request.concurrency > maxConcurrency.value) {
      return `并发数必须在 1~${maxConcurrency.value} 之间`
    }
    return ''
  }

  async function start(): Promise<void> {
    if (!canStart.value) return
    const queuedItems = items.value.filter((item) => item.status === 'queued')
    const files = queuedItems.map((item) => item.file)
    const request = buildRequest(files)
    const problem = validateRequest(request, queuedItems.length)
    if (problem) {
      toast('warn', problem)
      return
    }
    starting.value = true
    try {
      const result = await api.UpscaleBatch(request)
      runningJobId.value = result.jobId
      job.value = null
      if (result.skipped.length > 0) {
        const skippedKeys = new Set(result.skipped.map((file) => file.trim().toLowerCase()))
        items.value = items.value.map((item) =>
          skippedKeys.has(item.file.trim().toLowerCase())
            ? { ...item, status: 'skipped', error: '已跳过：不存在 / 非图片 / 重复路径' }
            : item,
        )
        toast('warn', `${result.skipped.length} 个文件被跳过（不在队列中或不是图片）`)
      }
      pushLog(
        'info',
        `任务 ${result.jobId} 已提交：${result.queued} 张，并发 ${request.concurrency}，x${request.scale}`,
      )
      if (result.queued === 0) {
        starting.value = false
        toast('warn', '没有文件进入处理队列')
      }
    } catch (error) {
      starting.value = false
      toast('error', `提交任务失败：${errorMessage(error)}`)
    }
  }

  async function cancel(): Promise<void> {
    const jobId = job.value?.jobId || runningJobId.value
    if (!jobId) {
      toast('warn', '当前没有可取消的任务')
      return
    }
    try {
      const ok = await api.CancelJob(jobId)
      pushLog(ok ? 'warn' : 'error', ok ? `已请求取消任务 ${jobId}` : `取消任务失败：${jobId}`)
      if (!ok) toast('error', '取消任务失败：任务可能已结束')
    } catch (error) {
      toast('error', `取消任务失败：${errorMessage(error)}`)
    }
  }

  /* --------------------------------------------------------------- 结果展示 */
  function addRecent(result: RecentResult): void {
    const key = result.path.toLowerCase()
    const deduped = recent.value.filter((entry) => entry.path.toLowerCase() !== key)
    recent.value = [result, ...deduped].slice(0, MAX_RECENT_RESULTS)
  }

  function openCompare(item: QueueItem): void {
    if (!item.output) return
    compareTarget.value = { file: item.file, output: item.output, name: item.name }
  }

  /** 最近结果卡片直接打开对比（没有 QueueItem 时的入口）。 */
  function openComparePaths(file: string, output: string, name: string): void {
    if (!output) return
    compareTarget.value = { file, output, name }
  }

  function closeCompare(): void {
    compareTarget.value = null
  }

  function clearLogs(): void {
    logs.value = []
  }

  function selectModel(id: string): void {
    selectedModelId.value = id
    const model = models.value.find((entry) => entry.id === id)
    if (model && model.scale > 0) scale.value = clamp(model.scale, 1, 8)
  }

  return {
    // 状态
    appInfo,
    models,
    modelsLoading,
    loading,
    initError,
    selectedModelId,
    selectedModel,
    scale,
    effectiveScale,
    outputDir,
    concurrency,
    maxConcurrency,
    tileSize,
    overlap,
    threads,
    fast,
    inhibitRequest,
    items,
    logs,
    toasts,
    inhibitStatus,
    job,
    runningJobId,
    starting,
    recent,
    compareTarget,
    // 派生
    queueSummary,
    pending,
    running,
    canStart,
    engineLabel,
    anyInhibit,
    isDesktopMode,
    isMockMode: isMockMode(),
    // 方法
    init,
    dispose,
    loadModels,
    pushLog,
    toast,
    dismissToast,
    addFiles,
    pickImages,
    pickOutputDir,
    removeItem,
    clearFinished,
    setConcurrency,
    updateInhibit,
    refreshInhibit,
    reveal,
    openDataDir,
    start,
    cancel,
    openCompare,
    openComparePaths,
    closeCompare,
    clearLogs,
    selectModel,
    itemPercent,
  }
}

export type ChronosStore = ReturnType<typeof useChronos>
