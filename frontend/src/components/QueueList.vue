<script setup lang="ts">
import { computed, ref } from 'vue'
import Icon from './Icon.vue'
import type { ChronosStore } from '@/composables/useChronos'
import { itemPercent, type QueueItem } from '@/lib/queue'
import { STATUS_CLASS, STATUS_LABEL } from '@/lib/labels'
import { formatDuration, formatPercent } from '@/lib/format'
import { toImageSrc } from '@/lib/media'

const props = defineProps<{ store: ChronosStore }>()
const { items, queueSummary, removeItem, clearFinished, reveal, openCompare, running } = props.store

const brokenThumbs = ref<string[]>([])

const finishedCount = computed(
  () => queueSummary.value.done + queueSummary.value.failed + queueSummary.value.cancelled + queueSummary.value.skipped,
)

function markBroken(id: string): void {
  if (!brokenThumbs.value.includes(id)) brokenThumbs.value = brokenThumbs.value.concat(id)
}

function thumb(item: QueueItem): string {
  return toImageSrc(item.file, 'orig')
}

function canRemove(item: QueueItem): boolean {
  return item.status !== 'running'
}

function metaText(item: QueueItem): string {
  if (item.status === 'done') {
    const dims = item.width > 0 && item.height > 0 ? `${item.width}×${item.height}` : '尺寸未知'
    return item.output ? `${dims} · ${item.output}` : dims
  }
  if (item.status === 'failed' || item.status === 'cancelled' || item.status === 'skipped') {
    return item.error || STATUS_LABEL[item.status]
  }
  if (item.status === 'running') {
    const tile = item.total > 0 ? `${item.done}/${item.total} tiles` : '准备中'
    return `${tile}${item.elapsedMs > 0 ? ` · ${formatDuration(item.elapsedMs)}` : ''}`
  }
  return item.dir || '等待调度'
}

function progressOf(item: QueueItem): number {
  return Math.round(itemPercent(item) * 100)
}
</script>

<template>
  <section class="panel flex min-h-0 flex-col">
    <div class="panel-head">
      <div class="panel-title">
        <Icon name="layers" :size="14" class="text-mute" />
        处理队列
        <span class="chip bg-overlay/5 text-mute-soft ring-overlay/10">{{ queueSummary.total }}</span>
        <span v-if="queueSummary.running > 0" class="chip bg-brand-500/10 text-brand-300 ring-brand-400/30">
          <span class="h-1.5 w-1.5 animate-pulse-soft rounded-full bg-brand-300" />
          {{ queueSummary.running }} 处理中
        </span>
      </div>
      <div class="flex items-center gap-1.5">
        <span v-if="queueSummary.done > 0" class="chip bg-emerald-500/10 text-emerald-300 ring-emerald-400/25">
          {{ queueSummary.done }} 完成
        </span>
        <span v-if="queueSummary.failed > 0" class="chip bg-rose-500/10 text-rose-300 ring-rose-400/25">
          {{ queueSummary.failed }} 失败
        </span>
        <button
          type="button"
          class="btn-ghost !px-2 !py-1 text-[11.5px]"
          :disabled="finishedCount === 0 || running"
          title="移除已结束（完成 / 失败 / 取消 / 跳过）的条目"
          @click="clearFinished()"
        >
          <Icon name="trash" :size="13" />
          清理已结束
        </button>
      </div>
    </div>

    <div v-if="items.length === 0" class="flex flex-col items-center justify-center gap-2 px-4 py-9 text-center">
      <div class="flex h-10 w-10 items-center justify-center rounded-xl border border-line bg-ink-900/60 text-mute-soft">
        <Icon name="image" :size="18" />
      </div>
      <p class="text-[12px] text-mute">队列还是空的</p>
      <p class="max-w-[320px] text-[11px] text-mute-soft">
        把图片拖到上方区域，或点击「选择图片」加入队列；同名输出会自动追加 -1 / -2 后缀。
      </p>
    </div>

    <ul v-else class="max-h-[268px] min-h-0 divide-y divide-line-soft overflow-y-auto">
      <li
        v-for="item in items"
        :key="item.id"
        class="group flex items-center gap-2.5 px-3 py-2 transition-colors hover:bg-overlay/[0.025]"
        :class="item.status === 'running' ? 'bg-brand-500/[0.04]' : ''"
      >
        <div class="relative h-9 w-9 shrink-0 overflow-hidden rounded-md border border-line bg-ink-900">
          <img
            v-if="!brokenThumbs.includes(item.id)"
            :src="thumb(item)"
            :alt="item.name"
            class="h-full w-full object-cover"
            loading="lazy"
            draggable="false"
            @error="markBroken(item.id)"
          />
          <div v-else class="flex h-full w-full items-center justify-center text-mute-soft">
            <Icon name="image" :size="14" />
          </div>
          <div
            v-if="item.status === 'running'"
            class="absolute inset-x-0 bottom-0 h-[2px] bg-ink-700"
            role="progressbar"
            :aria-valuenow="progressOf(item)"
            aria-valuemin="0"
            aria-valuemax="100"
          >
            <div class="h-full bg-brand-400 transition-[width] duration-300" :style="{ width: `${progressOf(item)}%` }" />
          </div>
        </div>

        <div class="min-w-0 flex-1">
          <div class="flex items-center gap-1.5">
            <span class="truncate text-[12.5px] font-medium text-mute-strong" :title="item.file">{{ item.name }}</span>
            <span class="chip shrink-0" :class="STATUS_CLASS[item.status]">
              <span
                v-if="item.status === 'running'"
                class="h-1.5 w-1.5 animate-pulse-soft rounded-full bg-current"
              />
              {{ STATUS_LABEL[item.status] }}
            </span>
            <span v-if="item.backend && item.status === 'done'" class="chip shrink-0 bg-overlay/5 text-mute-soft ring-overlay/10 font-mono">
              {{ item.backend }}
            </span>
          </div>
          <p
            class="mt-0.5 truncate text-[10.5px]"
            :class="
              item.status === 'failed'
                ? 'text-rose-300/90'
                : item.status === 'done'
                  ? 'text-mute-soft'
                  : 'text-mute-soft/80'
            "
            :title="metaText(item)"
          >
            {{ metaText(item) }}
          </p>
          <div v-if="item.status === 'running'" class="mt-1 flex items-center gap-2">
            <div class="h-1 flex-1 overflow-hidden rounded-full bg-ink-700">
              <div
                class="h-full rounded-full bg-gradient-to-r from-brand-500 to-brand-300 transition-[width] duration-300"
                :style="{ width: `${progressOf(item)}%` }"
              />
            </div>
            <span class="w-9 shrink-0 text-right font-mono text-[10px] text-brand-300">
              {{ formatPercent(item.done, item.total) }}
            </span>
          </div>
        </div>

        <div class="flex shrink-0 items-center gap-0.5 opacity-70 transition-opacity group-hover:opacity-100">
          <button
            v-if="item.status === 'done' && item.output"
            type="button"
            class="icon-btn"
            title="原图 / 结果对比"
            aria-label="打开对比"
            @click="openCompare(item)"
          >
            <Icon name="compare" :size="14" />
          </button>
          <button
            v-if="item.output || item.file"
            type="button"
            class="icon-btn"
            title="在资源管理器中显示"
            aria-label="在资源管理器中显示"
            @click="reveal(item.output || item.file)"
          >
            <Icon name="external" :size="14" />
          </button>
          <button
            type="button"
            class="icon-btn hover:!text-rose-300"
            :disabled="!canRemove(item)"
            :title="canRemove(item) ? '从队列移除' : '处理中的条目不能移除'"
            aria-label="从队列移除"
            @click="removeItem(item.id)"
          >
            <Icon name="close" :size="14" />
          </button>
        </div>
      </li>
    </ul>
  </section>
</template>
