<script setup lang="ts">
import { computed } from 'vue'
import Icon from './Icon.vue'
import type { ChronosStore } from '@/composables/useChronos'
import { backendLabel } from '@/lib/labels'

const props = defineProps<{ store: ChronosStore }>()
const emit = defineEmits<{ (event: 'close'): void }>()

const { appInfo, models, loadModels, openDataDir, previewMode, modeInfo, browseSupported, maxConcurrency } = props.store

const engine = computed(() => appInfo.value?.engine ?? null)
const rows = computed(() => [
  { label: '版本', value: appInfo.value?.version || '—' },
  { label: '提交', value: appInfo.value?.commit || '—' },
  { label: '构建时间', value: appInfo.value?.buildTime || '—' },
  { label: 'Go', value: appInfo.value?.goVersion || '—' },
  { label: '平台', value: appInfo.value?.platform || '—' },
  { label: '运行模式', value: modeInfo.value.label },
  { label: '传输', value: modeInfo.value.transport.toUpperCase() },
  { label: '目录浏览', value: browseSupported.value ? '支持（ListDirectory）' : '不支持' },
  { label: '原生库', value: appInfo.value ? (appInfo.value.nativeLoaded ? '已加载' : '未加载（回退）') : '—' },
  { label: '并发上限', value: String(maxConcurrency.value) },
  { label: '模型数量', value: String(models.value.length) },
])

function pathRow(label: string, value: string | undefined): { label: string; value: string } {
  return { label, value: value || '—' }
}
</script>

<template>
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-ink-950/70 p-4 backdrop-blur-sm" @click.self="emit('close')">
    <div class="w-full max-w-[560px] overflow-hidden rounded-xl border border-line bg-ink-850 shadow-2xl">
      <div class="flex items-center gap-2 border-b border-line-soft px-4 py-2.5">
        <Icon name="settings" :size="15" class="text-mute" />
        <span class="text-[13px] font-medium text-mute-strong">设置 / 关于</span>
        <span v-if="modeInfo.transport === 'http'" class="chip bg-sky-500/10 text-sky-300 ring-sky-400/25">浏览器模式</span>
        <span v-else-if="previewMode" class="chip bg-amber-500/10 text-amber-300 ring-amber-400/25">浏览器预览</span>
        <button type="button" class="icon-btn ml-auto" aria-label="关闭设置" title="关闭（Esc）" @click="emit('close')">
          <Icon name="close" :size="16" />
        </button>
      </div>

      <div class="max-h-[70vh] overflow-y-auto px-4 py-3">
        <h3 class="text-[11px] font-semibold uppercase tracking-wider text-mute-soft">版本信息</h3>
        <dl class="mt-1.5 grid grid-cols-2 gap-x-4 gap-y-1 text-[11.5px]">
          <div v-for="row in rows" :key="row.label" class="flex items-baseline justify-between gap-2 border-b border-line-soft/60 py-0.5">
            <dt class="shrink-0 text-mute-soft">{{ row.label }}</dt>
            <dd class="min-w-0 truncate font-mono text-[11px] text-mute-strong" :title="row.value">{{ row.value }}</dd>
          </div>
        </dl>

        <h3 class="mt-4 text-[11px] font-semibold uppercase tracking-wider text-mute-soft">引擎</h3>
        <div class="mt-1.5 rounded-lg border border-line-soft bg-ink-900/50 p-2.5 text-[11.5px]">
          <div class="flex items-center gap-2">
            <span class="chip bg-brand-500/10 text-brand-300 ring-brand-400/30">{{ backendLabel(engine?.backend || '') }}</span>
            <span class="chip bg-white/5 text-mute ring-white/10">{{ engine?.device || 'n/a' }}</span>
            <span class="chip bg-white/5 font-mono text-mute-soft ring-white/10">ABI {{ engine?.abiVersion ?? '—' }}</span>
          </div>
          <p class="mt-1.5 text-[11px] text-mute-soft">{{ engine?.detail || '无引擎详情' }}</p>
        </div>

        <h3 class="mt-4 text-[11px] font-semibold uppercase tracking-wider text-mute-soft">路径</h3>
        <div class="mt-1.5 space-y-1 text-[11.5px]">
          <div
            v-for="row in [
              pathRow('数据目录', appInfo?.dataDir),
              pathRow('模型目录', appInfo?.modelDir),
              pathRow('默认输出', appInfo?.outputDir),
            ]"
            :key="row.label"
            class="flex items-baseline gap-2"
          >
            <span class="w-16 shrink-0 text-mute-soft">{{ row.label }}</span>
            <span class="min-w-0 flex-1 truncate font-mono text-[10.5px] text-mute-strong" :title="row.value">{{ row.value }}</span>
          </div>
        </div>

        <div class="mt-3 flex flex-wrap gap-1.5">
          <button type="button" class="btn-ghost" @click="openDataDir()">
            <Icon name="folder" :size="13" />
            打开数据目录
          </button>
          <button type="button" class="btn-ghost" @click="loadModels(true)">
            <Icon name="refresh" :size="13" />
            重新扫描模型
          </button>
        </div>

        <p class="mt-4 border-t border-line-soft pt-2.5 text-[10.5px] leading-relaxed text-mute-soft">
          Chronos 分层：Vue 3 UI → Wails 绑定（JSON RPC）→ Go 调度层 → CGO → C++ 算力层。
          前端只传文件路径，图像读写在 C++ 内部完成；并发上限由配置层与 UI 双重约束为 8。
        </p>
      </div>
    </div>
  </div>
</template>
