<script setup lang="ts">
import Icon from './Icon.vue'
import type { ChronosStore, ToastKind } from '@/composables/useChronos'

const props = defineProps<{ store: ChronosStore }>()
const { toasts, dismissToast } = props.store

const TONE: Record<ToastKind, { border: string; text: string; icon: string }> = {
  info: { border: 'border-sky-400/30', text: 'text-sky-200', icon: 'info' },
  success: { border: 'border-emerald-400/30', text: 'text-emerald-200', icon: 'check' },
  warn: { border: 'border-amber-400/35', text: 'text-amber-200', icon: 'alert' },
  error: { border: 'border-rose-400/35', text: 'text-rose-200', icon: 'x-circle' },
}
</script>

<template>
  <div class="pointer-events-none fixed right-3 top-[60px] z-[60] flex w-[320px] max-w-[calc(100vw-24px)] flex-col gap-2" aria-live="polite">
    <div
      v-for="entry in toasts"
      :key="entry.id"
      class="pointer-events-auto flex animate-toast-in items-start gap-2 rounded-lg border bg-ink-850/95 px-3 py-2 shadow-panel backdrop-blur"
      :class="TONE[entry.kind].border"
    >
      <Icon :name="TONE[entry.kind].icon" :size="14" class="mt-0.5 shrink-0" :class="TONE[entry.kind].text" />
      <p class="min-w-0 flex-1 whitespace-pre-wrap break-words text-[11.5px]" :class="TONE[entry.kind].text">
        {{ entry.message }}
      </p>
      <button
        type="button"
        class="icon-btn !h-5 !w-5 shrink-0"
        aria-label="关闭提示"
        @click="dismissToast(entry.id)"
      >
        <Icon name="close" :size="11" />
      </button>
    </div>
  </div>
</template>
