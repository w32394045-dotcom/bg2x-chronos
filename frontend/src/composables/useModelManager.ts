/**
 * 模型管理与加速状态的前端状态机（契约 v3）。
 *
 * 只依赖 `api` 与 `onEvent`，因此桌面（Wails）与浏览器（HTTP/SSE）行为一致；
 * mock 模式下同样可用（`vite dev` 可完整走一遍下载进度）。
 */
import { onMounted, onUnmounted, ref } from 'vue'
import { api, onEvent } from '@/lib/api'
import type { AccelStatus, ModelInfo, ModelPullState, ModelSelfTest } from '@/lib/types'

export function useModelManager() {
  /** modelId -> 最近一次下载状态 */
  const pulls = ref<Record<string, ModelPullState>>({})
  /** modelId -> 最近一次自检结果 */
  const tests = ref<Record<string, ModelSelfTest>>({})
  const accel = ref<AccelStatus | null>(null)
  const accelOpen = ref(false)
  const error = ref('')

  const offs: Array<() => void> = []

  function pullState(id: string): ModelPullState | null {
    return pulls.value[id] ?? null
  }

  function isPulling(id: string): boolean {
    const phase = pulls.value[id]?.phase
    return !!phase && !['done', 'failed', 'cancelled'].includes(phase)
  }

  function progress(id: string): number {
    const st = pulls.value[id]
    if (!st || st.total <= 0) return 0
    return Math.min(1, st.done / st.total)
  }

  async function install(model: ModelInfo): Promise<void> {
    error.value = ''
    try {
      pulls.value = { ...pulls.value, [model.id]: await api.PullModel({ modelId: model.id, mirror: '' }) }
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
    }
  }

  async function cancel(id: string): Promise<void> {
    try {
      await api.CancelPull(id)
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
    }
  }

  async function remove(id: string): Promise<void> {
    error.value = ''
    try {
      await api.RemoveModel(id)
      const next = { ...pulls.value }
      delete next[id]
      pulls.value = next
      await api.RefreshModels()
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
    }
  }

  async function selfTest(id: string): Promise<void> {
    error.value = ''
    try {
      tests.value = { ...tests.value, [id]: await api.SelfTestModel(id) }
      await api.RefreshModels()
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
    }
  }

  async function diagnostics(id: string): Promise<string> {    try {
      const diag = await api.GetModelDiagnostics(id)
      const lines = [
        `model: ${diag.modelId}`,
        `opset: ${diag.opset}`,
        `error: ${diag.error || '-'}`,
        ...diag.warnings.map((w) => `warning: ${w}`),
        ...diag.inputs.map((t) => `input: ${t.name} ${JSON.stringify(t.dims)} ${t.dtype}`),
        ...diag.outputs.map((t) => `output: ${t.name} ${JSON.stringify(t.dims)} ${t.dtype}`),
      ]
      return lines.join('\n')
    } catch (e) {
      return e instanceof Error ? e.message : String(e)
    }
  }

  async function copyDiagnostics(id: string): Promise<void> {
    const text = tests.value[id]?.diagnostics || (await diagnostics(id))
    try {
      await navigator.clipboard.writeText(text)
    } catch {
      // 无剪贴板权限时静默忽略（桌面 WebView 通常允许）
    }
  }

  /** 桌面原生文件选择；浏览器模式返回空串，由调用方改用路径输入/PathPicker。 */
  async function pickModelFile(): Promise<string> {
    try {
      return await api.PickModelFile()
    } catch {
      return ''
    }
  }

  /** 导入外部模型；成功后刷新列表并返回新模型（失败返回 null 并写入 error）。 */
  async function importModel(path: string, name = '', autoDetect = true): Promise<ModelInfo | null> {
    error.value = ''
    if (!path.trim()) {
      error.value = '请选择或输入 .onnx 模型路径'
      return null
    }
    try {
      const info = await api.ImportModel({ path: path.trim(), name: name.trim(), autoDetect })
      await api.RefreshModels()
      return info
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
      return null
    }
  }

  async function loadAccel(): Promise<void> {
    try {
      accel.value = await api.GetAccelStatus()
    } catch {
      accel.value = null
    }
  }

  async function setAccelMode(mode: string): Promise<void> {
    try {
      accel.value = await api.SetAccelMode(mode)
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
    }
  }

  onMounted(() => {
    offs.push(
      onEvent<ModelPullState>('chronos:model', (st) => {
        pulls.value = { ...pulls.value, [st.modelId]: st }
        if (['done', 'failed', 'cancelled'].includes(st.phase)) {
          void api.RefreshModels()
        }
      }),
      onEvent<AccelStatus>('chronos:accel', (st) => {
        accel.value = st
      }),
    )
    void loadAccel()
  })

  onUnmounted(() => {
    for (const off of offs) off()
  })

  return {
    pulls,
    tests,
    accel,
    accelOpen,
    error,
    pullState,
    isPulling,
    progress,
    install,
    cancel,
    remove,
    selfTest,
    diagnostics,
    copyDiagnostics,
    pickModelFile,
    importModel,
    loadAccel,
    setAccelMode,
  }
}
