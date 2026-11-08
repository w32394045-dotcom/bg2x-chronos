<script setup lang="ts">
import { computed } from 'vue'
import Icon from './Icon.vue'
import type { ChronosStore } from '@/composables/useChronos'
import { THEME_LABEL, activeTheme, nextTheme, setTheme } from '@/lib/theme'

const props = defineProps<{
  store: ChronosStore
  settingsOpen: boolean
}>()

const emit = defineEmits<{ (event: 'toggle-settings'): void }>()

const { appInfo, engineLabel, browserMode, previewMode } = props.store

const version = computed(() => appInfo.value?.version || '0.1.0')
const engineTone = computed(() => {
  const backend = appInfo.value?.engine?.backend
  if (!backend) return 'text-mute ring-overlay/10'
  if (backend === 'openvino') return 'text-brand-300 ring-brand-400/30 bg-brand-500/10'
  if (backend === 'stub') return 'text-sky-300 ring-sky-400/25 bg-sky-500/10'
  return 'text-amber-300 ring-amber-400/25 bg-amber-500/10'
})

const themeIcon = computed(() =>
  activeTheme.value === 'dark' ? 'moon' : activeTheme.value === 'light' ? 'sun' : 'spark',
)
const themeTitle = computed(() => `切换主题（当前：${THEME_LABEL[activeTheme.value]}）`)
// 显式包一层 computed：模板里不依赖“导入的 ref 自动解包”这一隐式行为
const currentTheme = computed(() => activeTheme.value)

function cycleTheme(): void {
  setTheme(nextTheme(activeTheme.value))
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
        :class="engineTone"
        :title="appInfo?.engine?.detail || '引擎信息'"
      >
        <Icon name="cpu" :size="11" />
        {{ engineLabel }}
      </span>
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
