<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import Icon from './Icon.vue'
import type { ChronosStore } from '@/composables/useChronos'
import { LOG_LEVEL_LABEL } from '@/lib/labels'
import { formatClock } from '@/lib/format'

const props = defineProps<{ store: ChronosStore }>()
const { logs, clearLogs } = props.store

const expanded = ref(false)
const pinned = ref(true)
const listEl = ref<HTMLElement | null>(null)

const levelCount = computed(() => {
  let errors = 0
  let warns = 0
  for (const entry of logs.value) {
    if (entry.level === 'error') errors += 1
    else if (entry.level === 'warn') warns += 1
  }
  return { errors, warns }
})
const lastEntry = computed(() => logs.value[logs.value.length - 1] ?? null)

function onScroll(): void {
  const el = listEl.value
  if (!el) return
  pinned.value = el.scrollHeight - el.scrollTop - el.clientHeight < 24
}

watch(
  () => logs.value.length,
  async () => {
    if (!expanded.value || !pinned.value) return
    await nextTick()
    const el = listEl.value
    if (el) el.scrollTop = el.scrollHeight
  },
  { flush: 'post' },
)

watch(expanded, async (open) => {
  if (!open) return
  await nextTick()
  const el = listEl.value
  if (el) el.scrollTop = el.scrollHeight
})

function toggle(): void {
  expanded.value = !expanded.value
}

const LOG_LEVEL_NAME: Record<string, string> = { debug: 'DBG', info: 'INF', warn: 'WRN', error: 'ERR' }
</script>

<template>
  <section class="shrink-0 border-t border-line bg-ink-900/80 backdrop-blur">
    <button
      type="button"
      class="flex h-[30px] w-full items-center gap-2 px-4 text-left transition-colors hover:bg-white/[0.03]"
      :aria-expanded="expanded"
      aria-label="展开或折叠日志控制台"
      @click="toggle"
    >
      <Icon name="chevron" :size="12" class="text-mute transition-transform duration-150" :class="expanded ? '' : 'rotate-180'" />
      <span class="flex items-center gap-1.5 text-[11px] font-medium text-mute">
        <Icon name="layers" :size="12" />
        日志控制台
        <span class="font-mono text-[10px] text-mute-soft">{{ logs.length }}/500</span>
      </span>
      <span v-if="levelCount.errors > 0" class="chip bg-rose-500/10 text-rose-300 ring-rose-400/25">
        {{ levelCount.errors }} 错误
      </span>
      <span v-else-if="levelCount.warns > 0" class="chip bg-amber-500/10 text-amber-300 ring-amber-400/25">
        {{ levelCount.warns }} 警告
      </span>
      <span v-if="!expanded && lastEntry" class="ml-1 min-w-0 flex-1 truncate font-mono text-[10.5px] text-mute-soft">
        {{ formatClock(lastEntry.timeMs) }} {{ LOG_LEVEL_NAME[lastEntry.level] ?? lastEntry.level }} {{ lastEntry.msg }}
      </span>
      <span class="ml-auto flex shrink-0 items-center gap-1">
        <span class="text-[10px] text-mute-soft">{{ expanded ? '折叠' : '展开' }}</span>
      </span>
    </button>

    <div v-show="expanded" class="border-t border-line-soft">
      <div class="flex items-center justify-between px-3 py-1">
        <span class="text-[10px] text-mute-soft">最近 500 条 · 自动滚动跟随最新</span>
        <div class="flex items-center gap-1">
          <button type="button" class="btn-ghost !px-2 !py-0.5 text-[10.5px]" @click="clearLogs">
            <Icon name="trash" :size="11" />
            清空
          </button>
          <button type="button" class="icon-btn !h-6 !w-6" title="折叠" aria-label="折叠日志" @click="expanded = false">
            <Icon name="chevron" :size="12" />
          </button>
        </div>
      </div>
      <div
        ref="listEl"
        class="h-[148px] overflow-y-auto bg-ink-950/60 px-3 py-1.5 font-mono text-[11px] leading-[1.55]"
        role="log"
        aria-live="polite"
        @scroll="onScroll"
      >
        <p v-if="logs.length === 0" class="text-mute-soft">（暂无日志）</p>
        <p v-for="(entry, index) in logs" :key="`${entry.timeMs}-${index}`" class="whitespace-pre-wrap break-words">
          <span class="text-mute-soft">{{ formatClock(entry.timeMs) }}</span>
          <span class="mx-1" :class="LOG_LEVEL_LABEL[entry.level]?.text ?? 'text-mute'">
            {{ LOG_LEVEL_LABEL[entry.level]?.tag ?? entry.level }}
          </span>
          <span :class="LOG_LEVEL_LABEL[entry.level]?.text ?? 'text-mute-strong'">{{ entry.msg }}</span>
        </p>
      </div>
    </div>
  </section>
</template>
