<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import Icon from './Icon.vue'
import type { ChronosStore } from '@/composables/useChronos'

const props = defineProps<{ store: ChronosStore }>()
const { addFiles, pickImages, toast, queueSummary, canBrowse, browserMode, openImagePicker } = props.store

const dragging = ref(false)
const justAdded = ref(false)
let dragDepth = 0
let addedTimer: ReturnType<typeof setTimeout> | null = null

/** 只提取路径，不读任何字节（契约 §0 铁律 1）。 */
function extractPaths(data: DataTransfer | null): string[] {
  if (!data) return []
  const paths: string[] = []
  const files = Array.from(data.files ?? [])
  for (const file of files) {
    // Wails / Chromium 在桌面上可能把绝对路径挂在 File.path 上
    const candidate = (file as File & { path?: string }).path
    if (typeof candidate === 'string' && candidate.trim()) paths.push(candidate.trim())
  }
  if (paths.length === 0) {
    // 从资源管理器拖拽时可能只有 uri-list（file://C:/...）
    const text = data.getData('text/uri-list') || data.getData('text/plain')
    for (const rawLine of text.split(/\r?\n/)) {
      const line = rawLine.trim()
      if (!line || line.startsWith('#')) continue
      if (/^file:\/\//i.test(line)) {
        const decoded = decodeURIComponent(line.replace(/^file:\/\//i, ''))
        paths.push(decoded.replace(/\//g, '\\').replace(/^\\([A-Za-z]:)/, '$1'))
      } else if (/^[A-Za-z]:[\\/]/.test(line) || line.startsWith('/')) {
        paths.push(line)
      }
    }
  }
  return [...new Set(paths)]
}

function flashAdded(): void {
  justAdded.value = true
  if (addedTimer) clearTimeout(addedTimer)
  addedTimer = setTimeout(() => (justAdded.value = false), 700)
}

function handleDrop(event: DragEvent): void {
  event.preventDefault()
  dragDepth = 0
  dragging.value = false
  const paths = extractPaths(event.dataTransfer)
  if (paths.length === 0) {
    toast('warn', '无法从拖拽内容读取本地路径，请使用「选择图片」按钮')
    return
  }
  addFiles(paths)
  flashAdded()
}

function handleDragEnter(event: DragEvent): void {
  event.preventDefault()
  dragDepth += 1
  dragging.value = true
}

function handleDragLeave(event: DragEvent): void {
  event.preventDefault()
  dragDepth = Math.max(0, dragDepth - 1)
  if (dragDepth === 0) dragging.value = false
}

function handleDragOver(event: DragEvent): void {
  event.preventDefault()
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy'
}

function handlePaste(event: ClipboardEvent): void {
  const data = event.clipboardData
  if (!data) return
  const paths = extractPaths(data)
  if (paths.length === 0) return
  event.preventDefault()
  addFiles(paths)
  flashAdded()
}

async function choose(): Promise<void> {
  await pickImages()
  flashAdded()
}

onMounted(() => window.addEventListener('paste', handlePaste))
onUnmounted(() => {
  window.removeEventListener('paste', handlePaste)
  if (addedTimer) clearTimeout(addedTimer)
})
</script>

<template>
  <section
    class="panel relative cursor-pointer overflow-hidden px-4 py-5 transition-colors duration-150"
    :class="[
      dragging
        ? 'border-brand-400/70 bg-brand-500/[0.07]'
        : justAdded
          ? 'border-brand-500/40'
          : 'hover:border-line-strong',
    ]"
    role="button"
    tabindex="0"
    aria-label="添加图片：点击选择或拖拽文件"
    @click="choose"
    @keydown.enter.prevent="choose"
    @keydown.space.prevent="choose"
    @dragenter="handleDragEnter"
    @dragleave="handleDragLeave"
    @dragover="handleDragOver"
    @drop="handleDrop"
  >
    <div
      class="pointer-events-none absolute inset-0 opacity-40 transition-opacity"
      :class="dragging ? 'opacity-100' : ''"
      style="
        background-image: radial-gradient(420px 120px at 50% 0%, var(--c-drop-glow), transparent 70%);
      "
    />
    <div class="relative flex items-center gap-3.5">
      <div
        class="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl border border-dashed transition-colors"
        :class="dragging ? 'border-brand-300 text-brand-200' : 'border-line-strong text-mute'"
      >
        <Icon :name="dragging ? 'download' : 'image'" :size="20" />
      </div>
      <div class="min-w-0 flex-1">
        <p class="text-[13px] font-medium text-mute-strong">
          {{ dragging ? '松开鼠标即可加入队列' : '拖拽图片到这里，或点击选择' }}
        </p>
        <p class="mt-0.5 truncate text-[11px] text-mute-soft">
          {{
            browserMode
              ? '点击选择将打开服务端目录选择器 · 只传递文件路径，不读取像素'
              : '支持 PNG / JPEG / WebP / BMP · 只传递文件路径，不读取像素 · 可粘贴文件路径'
          }}
        </p>
      </div>
      <div class="hidden shrink-0 items-center gap-1.5 sm:flex">
        <span v-if="queueSummary.total > 0" class="chip bg-overlay/5 text-mute ring-overlay/10">
          队列 {{ queueSummary.total }}
        </span>
        <button
          v-if="canBrowse && !browserMode"
          type="button"
          class="btn-ghost"
          tabindex="-1"
          title="浏览服务端目录（ListDirectory）"
          @click.stop="openImagePicker()"
        >
          <Icon name="folder" :size="14" />
          浏览
        </button>
        <button type="button" class="btn-ghost" tabindex="-1" @click.stop="choose">
          <Icon name="plus" :size="14" />
          选择图片
        </button>
      </div>
    </div>
  </section>
</template>
