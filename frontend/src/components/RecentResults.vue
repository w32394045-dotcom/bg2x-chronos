<script setup lang="ts">
import { ref } from 'vue'
import Icon from './Icon.vue'
import type { ChronosStore } from '@/composables/useChronos'
import { gradientDataUrl, toImageSrc } from '@/lib/media'
import { fileLabel } from '@/lib/queue'
import { formatClock } from '@/lib/format'

const props = defineProps<{ store: ChronosStore }>()
const { recent } = props.store

const emit = defineEmits<{
  (event: 'open', payload: { file: string; output: string; name: string }): void
}>()

const broken = ref<string[]>([])

function srcFor(path: string): string {
  if (broken.value.includes(path)) return gradientDataUrl(path, 'out')
  return toImageSrc(path, 'out')
}

function markBroken(path: string): void {
  if (!broken.value.includes(path)) broken.value = broken.value.concat(path)
}

function open(path: string, source: string): void {
  emit('open', { file: source, output: path, name: fileLabel(path) })
}
</script>

<template>
  <section class="panel">
    <div class="panel-head">
      <div class="panel-title">
        <Icon name="grid" :size="14" class="text-mute" />
        最近结果
        <span class="chip bg-overlay/5 text-mute-soft ring-overlay/10">{{ recent.length }}</span>
      </div>
      <span class="text-[10.5px] text-mute-soft">点击缩略图打开对比</span>
    </div>
    <div class="panel-body">
      <div v-if="recent.length === 0" class="flex flex-col items-center justify-center gap-1.5 py-6 text-center">
        <div class="flex h-9 w-9 items-center justify-center rounded-xl border border-line bg-ink-900/60 text-mute-soft">
          <Icon name="grid" :size="16" />
        </div>
        <p class="text-[11.5px] text-mute">还没有输出结果</p>
        <p class="text-[10.5px] text-mute-soft">任务完成后会在这里生成可对比的缩略图</p>
      </div>
      <div v-else class="grid grid-cols-2 gap-2 sm:grid-cols-3 xl:grid-cols-4">
        <button
          v-for="entry in recent"
          :key="entry.path"
          type="button"
          class="group relative overflow-hidden rounded-lg border border-line bg-ink-900 text-left transition-all hover:border-brand-500/50 hover:shadow-glow"
          :title="entry.path"
          @click="open(entry.path, entry.source)"
        >
          <div class="aspect-[4/3] w-full overflow-hidden">
            <img
              :src="srcFor(entry.path)"
              :alt="fileLabel(entry.path)"
              class="h-full w-full object-cover transition-transform duration-200 group-hover:scale-[1.04]"
              loading="lazy"
              draggable="false"
              @error="markBroken(entry.path)"
            />
          </div>
          <div class="absolute inset-x-0 bottom-0 bg-gradient-to-t from-ink-950/95 to-transparent px-1.5 pb-1 pt-3">
            <p class="truncate text-[10.5px] text-mute-strong">{{ fileLabel(entry.path) }}</p>
            <p class="flex items-center gap-1 truncate text-[9.5px] text-mute-soft">
              <span class="font-mono">x{{ entry.scale }}</span>
              <span v-if="entry.width > 0" class="font-mono">{{ entry.width }}×{{ entry.height }}</span>
              <span class="ml-auto font-mono">{{ formatClock(entry.at) }}</span>
            </p>
          </div>
          <span
            class="absolute right-1.5 top-1.5 flex h-5 w-5 items-center justify-center rounded-md bg-ink-950/70 text-brand-200 opacity-0 transition-opacity group-hover:opacity-100"
          >
            <Icon name="compare" :size="12" />
          </span>
        </button>
      </div>
    </div>
  </section>
</template>
