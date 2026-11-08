<script setup lang="ts">
import { computed } from 'vue'
import Icon from './Icon.vue'
import type { ChronosStore } from '@/composables/useChronos'
import { useModelManager } from '@/composables/useModelManager'
import { THEME_LABEL, activeTheme, nextTheme, setTheme } from '@/lib/theme'

const props = defineProps<{
  store: ChronosStore
  settingsOpen: boolean
}>()

const emit = defineEmits<{ (event: 'toggle-settings'): void }>()

const { appInfo, engineLabel, browserMode, previewMode } = props.store

const mm = useModelManager()
const accelOpen = mm.accelOpen
const accelStatus = computed(() => mm.accel.value)
const accelReason = computed(
  () => accelStatus.value?.fallbackReason || activeEngine.value?.fallbackReason || '',
)
const accelAvailable = computed(() => accelStatus.value?.available ?? ['CPU'])
const accelMode = computed(() => accelStatus.value?.mode ?? 'auto')
const gpuRuntime = computed(() => accelStatus.value?.gpuRuntime ?? null)
const gpuInstalling = computed(() =>
  ['resolving', 'downloading', 'installing'].includes(accelStatus.value?.installPhase ?? ''),
)
const gpuInstallFailed = computed(() => accelStatus.value?.installPhase === 'failed')
const gpuInstallPercent = computed(() => {
  const status = accelStatus.value
  if (!status) return 0
  if (status.installPhase === 'installing') return 99
  const total = status.installTotal || 0
  if (total <= 0) return 0
  return Math.min(100, Math.round(((status.installDone || 0) / total) * 100))
})
const canInstallGpu = computed(
  () => !!gpuRuntime.value?.installable && !gpuRuntime.value?.installed && !gpuInstalling.value,
)
const gpuRuntimeLabel = computed(() => {
  const component = gpuRuntime.value
  if (!component) return '未知'
  const experimental = component.experimental ? ' · exp' : ''
  if (component.installed) return `已安装 ${component.version}${experimental}`
  if (component.installable) return `未安装（约 ${component.sizeMb} MB${experimental}）`
  return '此平台不需要'
})
const gpuFlavorLabel = computed(() => {
  const flavor = gpuRuntime.value?.flavor
  if (flavor === 'cuda') return 'CUDA'
  if (flavor === 'openvino') return 'OpenVINO'
  return 'DirectML'
})
const restartRequired = computed(() => accelStatus.value?.restartRequired === true)

async function installGpu(): Promise<void> {
  await mm.installAccelRuntime()
}

const engineToneFallback = computed(() => {
  const backend = appInfo.value?.engine?.backend
  if (!backend) return 'text-mute ring-overlay/10'
  if (backend === 'ort' || backend === 'openvino') return 'text-brand-300 ring-brand-400/30 bg-brand-500/10'
  if (backend === 'stub') return 'text-sky-300 ring-sky-400/25 bg-sky-500/10'
  return 'text-amber-300 ring-amber-400/25 bg-amber-500/10'
})

/** 真实加速状态：优先用 GetAccelStatus 的 Active，退回启动时的 engine 快照。 */
const activeEngine = computed(() => mm.accel.value?.active ?? appInfo.value?.engine ?? null)
const isGpu = computed(() => activeEngine.value?.device === 'GPU')
const accelLabel = computed(() => {
  const engine = activeEngine.value
  if (!engine) return 'n/a'
  const ep = engine.ep || 'CPU'
  if (engine.device === 'GPU') return `GPU · ${ep}`
  return `CPU · ${ep}`
})
const accelTone = computed(() => {
  if (!activeEngine.value) return 'text-mute ring-overlay/10'
  if (isGpu.value) return 'text-emerald-300 ring-emerald-400/25 bg-emerald-500/10'
  const reason = mm.accel.value?.fallbackReason || activeEngine.value.fallbackReason || ''
  if (reason && mm.accel.value?.gpuUsable !== true) return 'text-amber-300 ring-amber-400/25 bg-amber-500/10'
  return 'text-mute ring-overlay/10'
})
const accelTitle = computed(() => {
  const engine = activeEngine.value
  if (!engine) return '加速状态未知'
  const parts = [`后端 ${engine.backend}`, `执行后端 ${engine.ep || 'CPU'}`, `设备 ${engine.device}`]
  if (engine.deviceName) parts.push(engine.deviceName)
  const reason = mm.accel.value?.fallbackReason || engine.fallbackReason
  if (reason) parts.push(`说明：${reason}`)
  return parts.join(' · ')
})

const version = computed(() => appInfo.value?.version || '0.1.0')
const themeIcon = computed(() =>
  activeTheme.value === 'dark' ? 'moon' : activeTheme.value === 'light' ? 'sun' : 'spark',
)
const themeTitle = computed(() => `切换主题（当前：${THEME_LABEL[activeTheme.value]}）`)
// 显式包一层 computed：模板里不依赖“导入的 ref 自动解包”这一隐式行为
const currentTheme = computed(() => activeTheme.value)

function cycleTheme(): void {
  setTheme(nextTheme(activeTheme.value))
}

function toggleAccel(): void {
  mm.accelOpen.value = !mm.accelOpen.value
}

async function setMode(mode: string): Promise<void> {
  await mm.setAccelMode(mode)
}
</script>

<template>
  <header
    class="flex h-[52px] shrink-0 items-center justify-between gap-3 border-b border-line bg-ink-900/80 px-4 backdrop-blur"
  >
    <div class="flex min-w-0 items-center gap-2.5">
      <div
        class="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg border border-brand-500/30 bg-brand-500/10 text-brand-300"
      >
        <Icon name="clock" :size="18" :stroke-width="1.8" />
      </div>
      <div class="min-w-0 leading-tight">
        <div class="flex items-center gap-2">
          <h1 class="truncate text-[14px] font-semibold tracking-wide text-fg">Chronos 超分工作站</h1>
          <span class="chip bg-overlay/5 text-mute-soft ring-overlay/10 font-mono">v{{ version }}</span>
        </div>
        <p class="truncate text-[11px] text-mute-soft">
          本地图片无损放大 · tile 分片推理 · 并发调度
        </p>
      </div>
    </div>

    <div class="flex shrink-0 items-center gap-2">
      <span
        v-if="browserMode"
        class="chip bg-sky-500/10 text-sky-300 ring-sky-400/25"
        title="浏览器模式：页面由本地 Go 服务提供（POST /api + SSE），不依赖 WebView2 / WebKit"
      >
        <Icon name="external" :size="10" />
        浏览器模式
      </span>
      <span
        v-else-if="previewMode"
        class="chip bg-amber-500/10 text-amber-300 ring-amber-400/25"
        title="浏览器预览模式：所有数据来自本地 mock，不会真正推理"
      >
        预览模式
      </span>
      <span
        class="chip max-w-[210px] truncate font-mono"
        :class="engineToneFallback"
        :title="appInfo?.engine?.detail || '引擎信息'"
      >
        <Icon name="cpu" :size="11" />
        {{ engineLabel }}
      </span>
      <div class="relative">
        <button
          type="button"
          class="chip font-mono"
          :class="accelTone"
          :title="accelTitle"
          aria-label="加速状态"
          @click="toggleAccel"
        >
          <Icon :name="isGpu ? 'spark' : 'cpu'" :size="11" />
          {{ accelLabel }}
        </button>
        <div
          v-if="accelOpen"
          class="panel absolute right-0 top-9 z-40 w-[268px] p-3 text-[11px] leading-relaxed text-mute"
        >
          <div class="mb-1.5 flex items-center justify-between">
            <span class="font-semibold text-mute-strong">加速状态</span>
            <span class="chip bg-overlay/5 text-mute-soft ring-overlay/10">{{ accelLabel }}</span>
          </div>
          <div class="space-y-1">
            <div>可用后端：<span class="text-mute-strong">{{ accelAvailable.join(' / ') }}</span></div>
            <div>设备：<span class="text-mute-strong">{{ activeEngine?.deviceName || '未报告' }}</span></div>
            <div>加速方式：<span class="text-mute-strong">{{ accelMode === 'cpu' ? '仅 CPU' : '自动' }}</span></div>
            <div v-if="accelReason" class="text-amber-300">说明：{{ accelReason }}</div>
          </div>
          <div class="mt-2.5 grid grid-cols-2 gap-1.5">
            <button
              type="button"
              class="btn-ghost"
              :class="accelMode !== 'cpu' ? 'border-brand-500/50 text-brand-300' : ''"
              @click="setMode('auto')"
            >
              自动
            </button>
            <button
              type="button"
              class="btn-ghost"
              :class="accelMode === 'cpu' ? 'border-brand-500/50 text-brand-300' : ''"
              @click="setMode('cpu')"
            >
              仅 CPU
            </button>
          </div>
          <div class="mt-2.5 border-t border-line-soft pt-2">
            <div class="flex items-center justify-between">
              <span>GPU 运行时</span>
              <span class="text-mute-strong">{{ gpuRuntimeLabel }}</span>
            </div>
            <div v-if="gpuInstalling" class="mt-1.5">
              <div class="h-1 w-full overflow-hidden rounded-full bg-ink-700">
                <div class="h-full bg-brand-400 transition-[width] duration-200" :style="{ width: `${gpuInstallPercent}%` }" />
              </div>
              <p class="mt-1 text-[10px] text-mute-soft">
                {{ accelStatus?.installPhase === 'installing' ? '正在解压安装…' : `下载中 ${gpuInstallPercent}%` }}`n                <span v-if="accelStatus?.installPart"> · {{ accelStatus.installPart }}</span>
              </p>
            </div>
            <button
              v-else-if="canInstallGpu"
              type="button"
              class="btn-ghost mt-1.5 w-full"
              @click="installGpu"
            >
              下载 GPU 运行时（{{ gpuFlavorLabel }} {{ gpuRuntime?.sizeMb }} MB）
            </button>
            <p v-if="gpuInstallFailed" class="mt-1 text-[10px] text-rose-300">
              安装失败：{{ accelStatus?.installError }}
            </p>
            <div
              v-if="accelStatus?.runtimeDisabled"
              class="mt-1 rounded-md bg-amber-500/10 px-2 py-1 text-[10px] text-amber-300"
            >
              已自动关闭：{{ accelStatus?.verifyError || '运行时自检未通过' }}
              <button type="button" class="ml-1 underline" @click="installGpu">重新尝试</button>
            </div>
            <p v-if="restartRequired" class="mt-1 text-[10px] text-amber-300">
              安装完成，重启应用后启用 GPU。
            </p>
            <p class="mt-1 text-[10px] leading-relaxed text-mute-soft">
              GPU 运行时是可选下载；没有它或没有 GPU 时会自动降级为 CPU，状态标不会造假。
            </p>
          </div>

          <p class="mt-2 text-[10px] text-mute-soft">
            切换只影响新任务；GPU 需要运行时带对应执行后端，否则自动降级为 CPU。
          </p>
        </div>
      </div>
      <button
        type="button"
        class="icon-btn"
        :title="themeTitle"
        aria-label="切换主题"
        :data-theme-current="currentTheme"
        @click="cycleTheme"
      >
        <Icon :name="themeIcon" :size="16" />
      </button>
      <button
        type="button"
        class="icon-btn"
        :class="settingsOpen ? 'bg-overlay/10 text-fg' : ''"
        aria-label="打开设置"
        title="设置 / 关于"
        @click="emit('toggle-settings')"
      >
        <Icon name="settings" :size="16" />
      </button>
    </div>
  </header>
</template>
