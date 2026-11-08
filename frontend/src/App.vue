<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import HeaderBar from './components/HeaderBar.vue'
import DropZone from './components/DropZone.vue'
import QueueList from './components/QueueList.vue'
import RecentResults from './components/RecentResults.vue'
import ControlPanel from './components/ControlPanel.vue'
import InhibitCard from './components/InhibitCard.vue'
import CompareSlider from './components/CompareSlider.vue'
import LogConsole from './components/LogConsole.vue'
import StatusBar from './components/StatusBar.vue'
import ToastHost from './components/ToastHost.vue'
import SettingsModal from './components/SettingsModal.vue'
import Icon from './components/Icon.vue'
import { useChronos } from '@/composables/useChronos'

const store = useChronos()
// 顶层解构让模板里直接拿到 ref 的值（脚本内部仍通过 store 调用方法）
const { compareTarget, initError, appInfo, canStart, start, closeCompare, reveal } = store

const settingsOpen = ref(false)

function closeTopLayer(): boolean {
  if (settingsOpen.value) {
    settingsOpen.value = false
    return true
  }
  if (compareTarget.value) {
    closeCompare()
    return true
  }
  return false
}

function isTypingTarget(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  if (!el) return false
  return (
    el.tagName === 'INPUT' ||
    el.tagName === 'TEXTAREA' ||
    el.tagName === 'SELECT' ||
    el.isContentEditable
  )
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    if (closeTopLayer()) event.preventDefault()
    return
  }
  if (event.key !== 'Enter' || event.ctrlKey || event.metaKey || event.altKey) return
  const target = event.target as HTMLElement | null
  if (isTypingTarget(target) || target?.tagName === 'BUTTON') return
  if (canStart.value) {
    event.preventDefault()
    void start()
  }
}

function openCompare(payload: { file: string; output: string; name: string }): void {
  store.openComparePaths(payload.file, payload.output, payload.name)
}

function revealPath(path: string): void {
  void reveal(path)
}

onMounted(() => {
  window.addEventListener('keydown', onKeydown)
  void store.init()
})

onUnmounted(() => {
  window.removeEventListener('keydown', onKeydown)
  store.dispose()
})
</script>

<template>
  <div class="flex h-full flex-col overflow-hidden">
    <HeaderBar :store="store" :settings-open="settingsOpen" @toggle-settings="settingsOpen = !settingsOpen" />

    <main class="min-h-0 flex-1 overflow-y-auto">
      <div class="mx-auto w-full max-w-[1120px] px-3 py-3 lg:px-4">
        <div
          v-if="initError"
          class="mb-3 flex items-center gap-2 rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-[11.5px] text-rose-200"
        >
          <Icon name="alert" :size="14" class="shrink-0" />
          <span class="min-w-0 flex-1 truncate">初始化失败：{{ initError }}</span>
          <button type="button" class="btn-ghost !border-rose-400/30 !px-2 !py-0.5 text-[11px]" @click="store.init()">
            重试
          </button>
        </div>

        <div class="grid grid-cols-1 items-start gap-3 lg:grid-cols-[minmax(0,1fr)_340px]">
          <div class="flex min-w-0 flex-col gap-3">
            <DropZone :store="store" />
            <QueueList :store="store" />
            <RecentResults :store="store" @open="openCompare" />
          </div>
          <div class="flex min-w-0 flex-col gap-3">
            <ControlPanel :store="store" />
            <InhibitCard :store="store" />
          </div>
        </div>

        <p class="mt-3 text-center text-[10px] text-mute-soft/70">
          Chronos v{{ appInfo?.version || '0.1' }} · 前端只传路径，像素读写全部在 C++ 侧完成
        </p>
      </div>
    </main>

    <LogConsole :store="store" />
    <StatusBar :store="store" />
    <ToastHost :store="store" />

    <CompareSlider
      v-if="compareTarget"
      :target="compareTarget"
      @close="closeCompare()"
      @reveal="revealPath"
    />

    <SettingsModal v-if="settingsOpen" :store="store" @close="settingsOpen = false" />
  </div>
</template>
