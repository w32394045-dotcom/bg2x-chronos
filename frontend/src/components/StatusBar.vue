<script setup lang="ts">
import { computed } from 'vue'
import Icon from './Icon.vue'
import type { ChronosStore } from '@/composables/useChronos'
import { backendLabel } from '@/lib/labels'

const props = defineProps<{ store: ChronosStore }>()
const { appInfo, concurrency, maxConcurrency, queueSummary, running, openDataDir, browserMode, previewMode } = props.store

const engineText = computed(() => {
  const engine = appInfo.value?.engine
  if (!engine) return '引擎未就绪'
  return `${backendLabel(engine.backend)} · ${engine.device || 'n/a'} · ABI ${engine.abiVersion}`
})
</script>

<template>
  <footer
    class="flex h-[26px] shrink-0 items-center gap-3 border-t border-line bg-ink-900/85 px-4 font-mono text-[10px] text-mute-soft"
  >
    <span
      v-if="browserMode"
      class="flex items-center gap-1 rounded px-1 text-sky-300"
      title="浏览器模式：页面由本地 Go 服务提供（POST /api + SSE）"
    >
      浏览器模式
    </span>
    <span v-else-if="previewMode" class="flex items-center gap-1 rounded px-1 text-amber-300" title="vite dev 预览，数据来自 mock">
      预览模式
    </span>
    <span class="flex items-center gap-1" title="Go 调度层维护的并发数（上限 8）">
      <Icon name="layers" :size="10" />
      并发 {{ concurrency }}/{{ maxConcurrency }}
      <span v-if="running" class="h-1.5 w-1.5 animate-pulse-soft rounded-full bg-brand-300" />
    </span>
    <span class="text-line-strong">|</span>
    <span class="flex items-center gap-1.5">
      队列 {{ queueSummary.total }}
      <span class="text-mute-soft">·</span>
      <span class="text-brand-300">{{ queueSummary.done }} 完成</span>
      <span v-if="queueSummary.failed > 0" class="text-rose-300">{{ queueSummary.failed }} 失败</span>
      <span class="text-mute-soft">{{ queueSummary.queued }} 等待</span>
    </span>
    <span class="text-line-strong">|</span>
    <span class="flex items-center gap-1 truncate" :title="appInfo?.engine?.detail || ''">
      <Icon name="cpu" :size="10" />
      {{ engineText }}
      <span
        v-if="appInfo"
        class="ml-1 rounded px-1"
        :class="appInfo.nativeLoaded ? 'text-emerald-300' : 'text-amber-300'"
      >
        {{ appInfo.nativeLoaded ? 'native' : 'go fallback' }}
      </span>
    </span>
    <span class="ml-auto flex items-center gap-1">
      <button
        type="button"
        class="flex items-center gap-1 rounded px-1 text-mute-soft transition-colors hover:text-brand-300"
        title="打开数据目录"
        @click="openDataDir()"
      >
        <Icon name="folder" :size="10" />
        <span class="max-w-[260px] truncate">{{ appInfo?.dataDir || '数据目录' }}</span>
      </button>
    </span>
  </footer>
</template>
