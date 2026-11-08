<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
import Icon from './Icon.vue'
import { gradientDataUrl, toImageSrc } from '@/lib/media'
import { fileLabel } from '@/lib/queue'

const props = defineProps<{
  target: { file: string; output: string; name: string }
}>()

const emit = defineEmits<{
  (event: 'close'): void
  (event: 'reveal', path: string): void
}>()

const position = ref(50)
const box = ref<HTMLElement | null>(null)
const dragging = ref(false)
const originalBroken = ref(false)
const resultBroken = ref(false)

const originalSrc = computed(() =>
  originalBroken.value ? gradientDataUrl(props.target.file || 'orig', 'orig') : toImageSrc(props.target.file, 'orig'),
)
const resultSrc = computed(() =>
  resultBroken.value ? gradientDataUrl(props.target.output || 'out', 'out') : toImageSrc(props.target.output, 'out'),
)
const title = computed(() => props.target.name || fileLabel(props.target.file))

function updateFromClientX(clientX: number): void {
  const el = box.value
  if (!el) return
  const rect = el.getBoundingClientRect()
  if (rect.width <= 0) return
  const pct = ((clientX - rect.left) / rect.width) * 100
  position.value = Math.min(100, Math.max(0, Number(pct.toFixed(2))))
}

function onPointerDown(event: PointerEvent): void {
  if (event.button !== 0) return
  dragging.value = true
  ;(event.currentTarget as HTMLElement).setPointerCapture?.(event.pointerId)
  updateFromClientX(event.clientX)
}

function onPointerMove(event: PointerEvent): void {
  if (!dragging.value) return
  updateFromClientX(event.clientX)
}

function onPointerUp(event: PointerEvent): void {
  dragging.value = false
  ;(event.currentTarget as HTMLElement).releasePointerCapture?.(event.pointerId)
}

function onKeydown(event: KeyboardEvent): void {
  const step = event.shiftKey ? 10 : 2
  if (event.key === 'ArrowLeft') {
    event.preventDefault()
    position.value = Math.max(0, position.value - step)
  } else if (event.key === 'ArrowRight') {
    event.preventDefault()
    position.value = Math.min(100, position.value + step)
  } else if (event.key === 'Home') {
    event.preventDefault()
    position.value = 0
  } else if (event.key === 'End') {
    event.preventDefault()
    position.value = 100
  }
}

onUnmounted(() => {
  dragging.value = false
})
</script>

<template>
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-ink-950/80 p-4 backdrop-blur-sm" @click.self="emit('close')">
    <div class="flex h-full max-h-[680px] w-full max-w-[980px] flex-col overflow-hidden rounded-xl border border-line bg-ink-850 shadow-2xl">
      <div class="flex items-center gap-2 border-b border-line-soft px-3.5 py-2.5">
        <Icon name="compare" :size="15" class="text-brand-300" />
        <span class="text-[12.5px] font-medium text-mute-strong">原图 / 超分结果对比</span>
        <span class="truncate text-[11px] text-mute-soft" :title="target.output">{{ title }}</span>
        <div class="ml-auto flex items-center gap-1.5">
          <button type="button" class="btn-ghost !px-2 !py-1 text-[11.5px]" @click="emit('reveal', target.output)">
            <Icon name="external" :size="13" />
            资源管理器
          </button>
          <button type="button" class="icon-btn" aria-label="关闭对比" title="关闭（Esc）" @click="emit('close')">
            <Icon name="close" :size="16" />
          </button>
        </div>
      </div>

      <div
        ref="box"
        class="relative min-h-0 flex-1 cursor-col-resize touch-none select-none overflow-hidden bg-ink-950"
        role="slider"
        tabindex="0"
        aria-label="对比分割线位置"
        :aria-valuenow="Math.round(position)"
        aria-valuemin="0"
        aria-valuemax="100"
        @pointerdown="onPointerDown"
        @pointermove="onPointerMove"
        @pointerup="onPointerUp"
        @pointercancel="onPointerUp"
        @keydown="onKeydown"
      >
        <!-- 底层：超分结果（右侧可见） -->
        <img
          :src="resultSrc"
          alt="超分结果"
          class="pointer-events-none absolute inset-0 h-full w-full object-contain"
          draggable="false"
          @error="resultBroken = true"
        />
        <!-- 上层：原图，按分割线裁切 -->
        <div class="absolute inset-0 overflow-hidden" :style="{ clipPath: `inset(0 ${100 - position}% 0 0)` }">
          <img
            :src="originalSrc"
            alt="原图"
            class="pointer-events-none absolute inset-0 h-full w-full object-contain"
            draggable="false"
            @error="originalBroken = true"
          />
        </div>

        <!-- 分割线与手柄 -->
        <div class="pointer-events-none absolute inset-y-0 w-px bg-brand-300/90 shadow-[0_0_12px_rgba(108,235,219,0.55)]" :style="{ left: `${position}%` }" />
        <div
          class="pointer-events-none absolute top-1/2 flex h-8 w-8 -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-full border border-brand-300/70 bg-ink-950/90 text-brand-200 shadow-lg"
          :style="{ left: `${position}%` }"
        >
          <Icon name="compare" :size="15" />
        </div>

        <span class="pointer-events-none absolute left-2.5 top-2.5 chip bg-ink-950/80 text-mute-strong ring-white/15">原图</span>
        <span class="pointer-events-none absolute right-2.5 top-2.5 chip bg-brand-500/20 text-brand-200 ring-brand-400/40">超分结果</span>
        <span class="pointer-events-none absolute bottom-2.5 left-1/2 -translate-x-1/2 chip bg-ink-950/80 font-mono text-mute ring-white/15">
          {{ Math.round(position) }}%
        </span>
      </div>

      <div class="flex items-center gap-3 border-t border-line-soft px-3.5 py-1.5 text-[10.5px] text-mute-soft">
        <span class="truncate font-mono" :title="target.file">原：{{ target.file }}</span>
        <span class="ml-auto shrink-0">拖动分割线或 ← → 微调（Shift 加速）</span>
      </div>
    </div>
  </div>
</template>
