<script setup lang="ts">
/**
 * 服务端路径选择器（浏览器模式下替代原生文件对话框）。
 *
 * 数据源：Go 的 ListDirectory(path) —— 空路径表示 home 目录。
 * 能力：目录导航（面包屑 / 上级 / 主目录）、图片多选、手动输入绝对路径、
 * 输出目录模式下确认当前目录。桌面 Wails 模式不使用该组件（保留原生对话框）。
 */
import { computed, onMounted, ref, watch } from 'vue'
import Icon from './Icon.vue'
import { api } from '@/lib/api'
import type { DirEntry, DirListing } from '@/lib/types'
import { formatBytes } from '@/lib/format'
import type { PickerMode, PickerSelection } from '@/composables/useChronos'

const props = defineProps<{
  mode: PickerMode
  /** 打开时的起始目录；空串 = Go 侧 home 目录 */
  initialPath?: string
}>()

const emit = defineEmits<{
  (event: 'confirm', selection: PickerSelection): void
  (event: 'close'): void
}>()

const cwd = ref('')
const parent = ref('')
const entries = ref<DirEntry[]>([])
const selected = ref<string[]>([])
const manual = ref('')
const loading = ref(false)
const loadError = ref('')

const isOutputMode = computed(() => props.mode === 'output')
const title = computed(() => (isOutputMode.value ? '选择输出目录' : '选择图片'))
const selectedCount = computed(() => selected.value.length)
const selectableCount = computed(() => entries.value.filter((entry) => !entry.isDir && entry.isImage).length)
const confirmDisabled = computed(() => {
  if (isOutputMode.value) return (manual.value.trim() || cwd.value) === ''
  return selectedCount.value === 0 && manual.value.trim() === ''
})

/** 面包屑：按当前路径的分隔符风格拼接，Windows 盘符保留。 */
const crumbs = computed<{ label: string; path: string }[]>(() => {
  const path = cwd.value
  if (!path) return []
  const sep = path.includes('\\') ? '\\' : '/'
  const parts = path.split(/[\\/]+/).filter(Boolean)
  const result: { label: string; path: string }[] = []
  let acc = ''
  if (path.startsWith('/')) {
    result.push({ label: '/', path: '/' })
    acc = '/'
  }
  for (const part of parts) {
    if (acc === '' && /^[A-Za-z]:$/.test(part)) acc = `${part}${sep}`
    else if (acc === '') acc = part
    else acc = acc.endsWith(sep) ? `${acc}${part}` : `${acc}${sep}${part}`
    result.push({ label: part, path: acc })
  }
  return result
})

/** 目录优先、图片其次、其它文件最后。 */
function sortEntries(list: DirEntry[]): DirEntry[] {
  return [...list].sort((a, b) => {
    const rank = (entry: DirEntry) => (entry.isDir ? 0 : entry.isImage ? 1 : 2)
    const diff = rank(a) - rank(b)
    return diff !== 0 ? diff : a.name.localeCompare(b.name)
  })
}

async function load(path: string): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const listing: DirListing = await api.ListDirectory(path)
    cwd.value = listing?.path || path
    parent.value = listing?.parent || ''
    entries.value = sortEntries(Array.isArray(listing?.entries) ? listing.entries : [])
    selected.value = []
    if (isOutputMode.value) manual.value = cwd.value
  } catch (error) {
    loadError.value =
      error instanceof Error
        ? error.message
        : typeof error === 'string'
          ? error
          : '读取目录失败'
    entries.value = []
  } finally {
    loading.value = false
  }
}

function navigate(entry: DirEntry): void {
  if (!entry.isDir) return
  void load(entry.path)
}

function goParent(): void {
  if (parent.value) void load(parent.value)
}

function goHome(): void {
  void load('')
}

function toggleImage(entry: DirEntry): void {
  if (entry.isDir || !entry.isImage) return
  selected.value = selected.value.includes(entry.path)
    ? selected.value.filter((path) => path !== entry.path)
    : selected.value.concat(entry.path)
}

function selectAllImages(): void {
  const all = entries.value.filter((entry) => !entry.isDir && entry.isImage).map((entry) => entry.path)
  selected.value = selected.value.length === all.length ? [] : all
}

/** 手动输入：换行 / 分号 / 竖线分隔，支持一次粘贴多条路径。 */
function manualPaths(): string[] {
  return manual.value
    .split(/[\r\n;|]+/)
    .map((line) => line.trim())
    .filter(Boolean)
}

function confirm(): void {
  if (isOutputMode.value) {
    const dir = manual.value.trim() || cwd.value
    if (!dir) {
      loadError.value = '请选择或输入一个输出目录'
      return
    }
    emit('confirm', { mode: 'output', files: [], dir })
    return
  }
  const typed = manualPaths()
  const files = typed.length > 0 ? typed : selected.value.slice()
  if (files.length === 0) {
    loadError.value = '请至少选择一个图片文件，或在下方手动输入路径'
    return
  }
  emit('confirm', { mode: 'images', files, dir: cwd.value })
}

function onManualKeydown(event: KeyboardEvent): void {
  if (event.key !== 'Enter' || event.ctrlKey || event.metaKey || event.altKey) return
  event.preventDefault()
  confirm()
}

function entryIcon(entry: DirEntry): string {
  if (entry.isDir) return 'folder'
  return entry.isImage ? 'image' : 'layers'
}

watch(
  () => props.initialPath,
  (path) => {
    void load(path ?? '')
  },
  { immediate: true },
)

onMounted(() => {
  // 让键盘用户一进来就能用 Tab 遍历列表
  const root = document.getElementById('chronos-path-picker')
  root?.focus()
})
</script>

<template>
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-ink-950/75 p-4 backdrop-blur-sm"
    @click.self="emit('close')"
  >
    <div
      id="chronos-path-picker"
      class="flex max-h-[82vh] w-full max-w-[720px] flex-col overflow-hidden rounded-xl border border-line bg-ink-850 shadow-2xl outline-none"
      role="dialog"
      aria-modal="true"
      :aria-label="title"
      tabindex="-1"
    >
      <!-- 标题栏 -->
      <div class="flex items-center gap-2 border-b border-line-soft px-4 py-2.5">
        <Icon :name="isOutputMode ? 'folder' : 'image'" :size="15" class="text-brand-300" />
        <span class="text-[13px] font-medium text-mute-strong">{{ title }}</span>
        <span class="chip bg-white/5 text-mute-soft ring-white/10">服务端浏览 · ListDirectory</span>
        <button
          type="button"
          class="icon-btn ml-auto"
          aria-label="关闭路径选择器"
          title="关闭（Esc）"
          @click="emit('close')"
        >
          <Icon name="close" :size="16" />
        </button>
      </div>

      <!-- 导航条 -->
      <div class="flex items-center gap-1.5 border-b border-line-soft px-3 py-1.5">
        <button
          type="button"
          class="btn-ghost !px-2 !py-1 text-[11px]"
          :disabled="!parent || loading"
          :title="parent || '已经是根目录'"
          @click="goParent"
        >
          <Icon name="chevron" :size="12" class="rotate-90" />
          上级
        </button>
        <button type="button" class="btn-ghost !px-2 !py-1 text-[11px]" :disabled="loading" title="主目录" @click="goHome">
          <Icon name="folder" :size="12" />
          主目录
        </button>
        <button
          type="button"
          class="icon-btn !h-6 !w-6"
          :disabled="loading"
          title="刷新当前目录"
          aria-label="刷新当前目录"
          @click="load(cwd)"
        >
          <Icon name="refresh" :size="12" :class="loading ? 'animate-spin' : ''" />
        </button>
        <div class="min-w-0 flex-1 overflow-x-auto whitespace-nowrap py-0.5 font-mono text-[10.5px] text-mute-soft">
          <template v-if="crumbs.length === 0">（读取中…）</template>
          <template v-for="(crumb, index) in crumbs" :key="crumb.path">
            <span v-if="index > 0" class="mx-0.5 text-line-strong">/</span>
            <button
              type="button"
              class="rounded px-0.5 transition-colors hover:text-brand-300"
              :class="index === crumbs.length - 1 ? 'text-mute-strong' : ''"
              :title="crumb.path"
              @click="load(crumb.path)"
            >
              {{ crumb.label }}
            </button>
          </template>
        </div>
      </div>

      <!-- 列表 -->
      <div class="min-h-[220px] flex-1 overflow-y-auto px-2 py-1">
        <div v-if="loading" class="flex items-center justify-center gap-2 py-10 text-[12px] text-mute">
          <span class="throbber" />
          正在读取目录…
        </div>
        <div v-else-if="loadError" class="flex items-start gap-2 rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2.5 text-[11.5px] text-rose-200">
          <Icon name="alert" :size="14" class="mt-0.5 shrink-0" />
          <span class="min-w-0 flex-1 break-words">{{ loadError }}</span>
          <button type="button" class="btn-ghost !border-rose-400/30 !px-2 !py-0.5 text-[11px]" @click="load(cwd)">
            重试
          </button>
        </div>
        <div v-else-if="entries.length === 0" class="flex flex-col items-center justify-center gap-1.5 py-10 text-center">
          <Icon name="folder" :size="18" class="text-mute-soft" />
          <p class="text-[11.5px] text-mute">目录为空</p>
          <p class="text-[10.5px] text-mute-soft">可以返回上级，或在下方手动输入路径</p>
        </div>
        <ul v-else class="space-y-0.5">
          <li v-for="entry in entries" :key="entry.path">
            <button
              v-if="entry.isDir"
              type="button"
              class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left transition-colors hover:bg-white/[0.05]"
              :title="entry.path"
              @click="navigate(entry)"
            >
              <Icon name="folder" :size="14" class="shrink-0 text-brand-300/80" />
              <span class="min-w-0 flex-1 truncate text-[12px] text-mute-strong">{{ entry.name }}</span>
              <Icon name="chevron" :size="12" class="shrink-0 -rotate-90 text-mute-soft" />
            </button>

            <button
              v-else
              type="button"
              class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left transition-colors"
              :class="
                entry.isImage
                  ? selected.includes(entry.path)
                    ? 'bg-brand-500/10'
                    : 'hover:bg-white/[0.05]'
                  : 'cursor-not-allowed opacity-45'
              "
              :disabled="!entry.isImage"
              :aria-pressed="entry.isImage ? selected.includes(entry.path) : undefined"
              :title="entry.isImage ? entry.path : '非图片文件，已跳过'"
              @click="toggleImage(entry)"
            >
              <Icon
                :name="entryIcon(entry)"
                :size="14"
                class="shrink-0"
                :class="entry.isImage ? 'text-sky-300/80' : 'text-mute-soft'"
              />
              <span class="min-w-0 flex-1 truncate text-[12px]" :class="entry.isImage ? 'text-mute-strong' : 'text-mute-soft'">
                {{ entry.name }}
              </span>
              <span class="shrink-0 font-mono text-[10px] text-mute-soft">{{ entry.isImage ? formatBytes(entry.size) : '—' }}</span>
              <span
                v-if="entry.isImage"
                class="flex h-4 w-4 shrink-0 items-center justify-center rounded border"
                :class="
                  selected.includes(entry.path)
                    ? 'border-brand-400 bg-brand-500 text-ink-950'
                    : 'border-line-strong text-transparent'
                "
              >
                <Icon name="check" :size="11" />
              </span>
            </button>
          </li>
        </ul>
      </div>

      <!-- 手动输入 -->
      <div class="border-t border-line-soft px-3 py-2">
        <label class="field-label" :for="'chronos-picker-manual'">
          <span>{{ isOutputMode ? '输出目录（可手动修改）' : '手动输入绝对路径' }}</span>
          <span class="text-[10px] text-mute-soft">
            {{ isOutputMode ? '留空则使用当前目录' : '每行一个，也可用 ; 分隔' }}
          </span>
        </label>
        <input
          id="chronos-picker-manual"
          v-model="manual"
          class="input mt-1 font-mono !text-[11px]"
          type="text"
          :placeholder="isOutputMode ? cwd || '例如 C:\\Users\\Public\\Chronos\\output' : '例如 C:\\Users\\Public\\Pictures\\a.jpg'"
          spellcheck="false"
          autocomplete="off"
          @keydown="onManualKeydown"
        />
      </div>

      <!-- 底部操作 -->
      <div class="flex items-center gap-2 border-t border-line-soft px-3 py-2">
        <button
          v-if="!isOutputMode"
          type="button"
          class="btn-ghost !px-2 !py-1 text-[11px]"
          :disabled="selectableCount === 0 || loading"
          @click="selectAllImages"
        >
          <Icon name="grid" :size="12" />
          {{ selectedCount === selectableCount && selectableCount > 0 ? '取消全选' : '全选本目录图片' }}
        </button>
        <span class="text-[11px] text-mute-soft">
          <template v-if="isOutputMode">当前目录：<span class="font-mono text-mute">{{ cwd || '—' }}</span></template>
          <template v-else>
            已选 <span class="font-mono text-brand-300">{{ selectedCount }}</span> 张
            <span v-if="selectedCount === 0 && manual.trim()"> · 将使用手动输入</span>
          </template>
        </span>
        <div class="ml-auto flex items-center gap-1.5">
          <button type="button" class="btn-ghost !px-2.5 !py-1 text-[11.5px]" @click="emit('close')">取消</button>
          <button
            type="button"
            class="btn-primary !px-3 !py-1 text-[11.5px]"
            :disabled="confirmDisabled || loading"
            @click="confirm"
          >
            <Icon :name="isOutputMode ? 'check' : 'plus'" :size="13" />
            {{ isOutputMode ? '使用此目录' : `加入队列${selectedCount > 0 ? `（${selectedCount}）` : ''}` }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
