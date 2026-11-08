<script setup lang="ts">
import { computed, ref } from 'vue'
import Icon from './Icon.vue'
import type { ChronosStore } from '@/composables/useChronos'
import {
  CONCURRENCY_SOFT_LIMIT,
  MODEL_KIND_HINT,
  MODEL_KIND_LABEL,
  modelKindClass,
} from '@/lib/labels'
import { formatBytes } from '@/lib/format'

const props = defineProps<{ store: ChronosStore }>()
const {
  models,
  modelsLoading,
  selectedModelId,
  selectedModel,
  scale,
  effectiveScale,
  outputDir,
  concurrency,
  maxConcurrency,
  tileSize,
  overlap,
  threads,
  fast,
  pending,
  running,
  canStart,
  starting,
  start,
  cancel,
  pickOutputDir,
  setOutputDir,
  canBrowse,
  browserMode,
  openOutputPicker,
  selectModel,
  loadModels,
} = props.store

const advancedOpen = ref(false)
const tilePresets = [128, 192, 256, 384, 512]
const scaleChoices = [1, 2, 3, 4]

const modelUnavailable = computed(
  () => !!selectedModel.value && (!selectedModel.value.installed || !selectedModel.value.path),
)
const concurrencyWarn = computed(() => concurrency.value > CONCURRENCY_SOFT_LIMIT)
const rangeStyle = computed(() => ({
  '--range-progress': `${maxConcurrency.value > 1 ? ((concurrency.value - 1) / (maxConcurrency.value - 1)) * 100 : 0}%`,
}))
const canClearOutput = computed(() => outputDir.value !== '')

function onConcurrencyInput(event: Event): void {
  const value = Number((event.target as HTMLInputElement).value)
  void props.store.setConcurrency(value)
}

function setTileSize(raw: string): void {
  const value = Number.parseInt(raw, 10)
  if (!Number.isFinite(value)) return
  tileSize.value = Math.min(1024, Math.max(32, value))
  if (overlap.value >= tileSize.value) overlap.value = Math.max(0, tileSize.value - 8)
}

function setOverlap(raw: string): void {
  const value = Number.parseInt(raw, 10)
  if (!Number.isFinite(value)) return
  overlap.value = Math.min(Math.max(0, value), Math.max(0, tileSize.value - 1))
}

function setThreads(raw: string): void {
  const value = Number.parseInt(raw, 10)
  if (!Number.isFinite(value)) return
  threads.value = Math.min(256, Math.max(0, value))
}

function clearOutputDir(): void {
  props.store.setOutputDir('')
}

/** 输出目录支持手动输入（浏览器模式没有原生目录对话框）。 */
function onOutputInput(event: Event): void {
  const value = (event.target as HTMLInputElement).value
  setOutputDir(value)
}

function onOutputKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    ;(event.target as HTMLInputElement).blur()
  }
}

function toggleFast(): void {
  fast.value = !fast.value
}

/** 显式函数而非模板内赋值：store 里解构出来的 ref 走 proxyRefs，直接赋值可读性差。 */
function setScale(value: number): void {
  scale.value = value
}
</script>

<template>
  <section class="panel">
    <div class="panel-head">
      <div class="panel-title">
        <Icon name="spark" :size="14" class="text-mute" />
        处理参数
      </div>
      <button
        type="button"
        class="icon-btn"
        :disabled="modelsLoading"
        title="重新扫描模型目录"
        aria-label="重新扫描模型目录"
        @click="loadModels(true)"
      >
        <Icon name="refresh" :size="14" :class="modelsLoading ? 'animate-spin' : ''" />
      </button>
    </div>

    <div class="panel-body space-y-3.5">
      <!-- 模型 -->
      <div>
        <div class="field-label">
          <span>模型</span>
          <span class="text-[10.5px] text-mute-soft">{{ models.length }} 个可用</span>
        </div>
        <div class="mt-1.5 max-h-[172px] space-y-1.5 overflow-y-auto pr-1">
          <button
            type="button"
            class="w-full rounded-lg border px-2.5 py-1.5 text-left transition-colors"
            :class="
              selectedModelId === ''
                ? 'border-brand-500/60 bg-brand-500/[0.08]'
                : 'border-line bg-ink-900/50 hover:border-line-strong'
            "
            @click="selectModel('')"
          >
            <div class="flex items-center gap-1.5">
              <span class="text-[12px] font-medium text-mute-strong">无模型（内置 Lanczos）</span>
              <span class="chip bg-overlay/5 text-mute ring-overlay/10">stub</span>
              <span class="ml-auto text-[10px] text-mute-soft">回退引擎</span>
            </div>
            <p class="mt-0.5 text-[10.5px] text-mute-soft">无 OpenVINO 模型时走纯 CPU 回退，任意倍率稳定输出。</p>
          </button>

          <button
            v-for="model in models"
            :key="model.id"
            type="button"
            class="w-full rounded-lg border px-2.5 py-1.5 text-left transition-colors"
            :class="
              selectedModelId === model.id
                ? 'border-brand-500/60 bg-brand-500/[0.08]'
                : 'border-line bg-ink-900/50 hover:border-line-strong'
            "
            :aria-pressed="selectedModelId === model.id"
            @click="selectModel(model.id)"
          >
            <div class="flex items-center gap-1.5">
              <span class="truncate text-[12px] font-medium text-mute-strong">{{ model.name }}</span>
              <span class="chip shrink-0" :class="modelKindClass(model.kind)">
                {{ MODEL_KIND_LABEL[model.kind] ?? model.kind }}
              </span>
              <span class="chip shrink-0 bg-overlay/5 font-mono text-mute-soft ring-overlay/10">x{{ model.scale }}</span>
              <span
                class="ml-auto chip shrink-0"
                :class="
                  model.installed
                    ? 'bg-emerald-500/10 text-emerald-300 ring-emerald-400/25'
                    : 'bg-amber-500/10 text-amber-300 ring-amber-400/25'
                "
              >
                {{ model.installed ? '已安装' : '未安装' }}
              </span>
            </div>
            <div class="mt-0.5 flex items-center gap-2 text-[10.5px] text-mute-soft">
              <span class="truncate">{{ model.description || MODEL_KIND_HINT[model.kind] }}</span>
              <span class="shrink-0 font-mono">{{ model.sizeMb > 0 ? formatBytes(model.sizeMb * 1024 * 1024) : '内置' }}</span>
            </div>
          </button>

          <p v-if="models.length === 0 && !modelsLoading" class="px-1 py-2 text-[11px] text-mute-soft">
            未发现模型：请检查数据目录 models/registry.json，或直接使用内置 Lanczos。
          </p>
        </div>
        <p v-if="modelUnavailable" class="mt-1.5 rounded-md bg-amber-500/10 px-2 py-1 text-[10.5px] text-amber-300">
          该模型尚未安装，任务会回退到内置 Lanczos（stub）引擎。
        </p>
      </div>

      <!-- 倍率 -->
      <div>
        <div class="field-label">
          <span>放大倍率</span>
          <span class="font-mono text-[11px] text-brand-300">x{{ effectiveScale }}</span>
        </div>
        <div v-if="selectedModel && selectedModel.installed" class="mt-1.5 flex items-center gap-2">
          <span class="chip bg-brand-500/10 text-brand-300 ring-brand-400/30">模型原生 x{{ selectedModel.scale }}</span>
          <span class="text-[10.5px] text-mute-soft">OpenVINO 模型以自身倍率为准</span>
        </div>
        <div v-else class="mt-1.5 flex gap-1.5">
          <button
            v-for="choice in scaleChoices"
            :key="choice"
            type="button"
            class="flex-1 rounded-md border py-1 text-[11.5px] font-mono transition-colors"
            :class="
              scale === choice
                ? 'border-brand-500/60 bg-brand-500/10 text-brand-200'
                : 'border-line bg-ink-900/50 text-mute hover:border-line-strong'
            "
            @click="setScale(choice)"
          >
            x{{ choice }}
          </button>
        </div>
      </div>

      <!-- 输出目录 -->
      <div>
        <div class="field-label">
          <span>输出目录</span>
          <button
            v-if="canClearOutput"
            type="button"
            class="text-[10.5px] text-mute-soft hover:text-mute-strong"
            title="留空则输出到输入文件同目录"
            @click="clearOutputDir"
          >
            恢复默认
          </button>
        </div>
        <div class="mt-1.5 flex items-stretch gap-1.5">
          <input
            class="input min-w-0 flex-1 truncate font-mono !py-1 !text-[10.5px] !text-mute-soft"
            type="text"
            :value="outputDir"
            placeholder="与输入同目录（默认）"
            spellcheck="false"
            autocomplete="off"
            aria-label="输出目录（可手动输入）"
            :title="outputDir"
            @input="onOutputInput"
            @keydown="onOutputKeydown"
          />
          <button
            v-if="canBrowse"
            type="button"
            class="btn-ghost shrink-0 !px-2.5 !py-1"
            title="浏览服务端目录（ListDirectory）"
            @click="openOutputPicker()"
          >
            <Icon name="folder" :size="13" />
            浏览
          </button>
          <button
            v-if="!browserMode"
            type="button"
            class="btn-ghost shrink-0 !px-2.5 !py-1"
            title="打开系统目录选择对话框"
            @click="pickOutputDir()"
          >
            <Icon name="folder" :size="13" />
            选择
          </button>
        </div>
        <p v-if="browserMode" class="mt-1 text-[10px] text-mute-soft">
          浏览器模式：可直接输入绝对路径，或点击「浏览」在服务端选择目录。
        </p>
      </div>

      <div class="divider !my-2.5" />

      <!-- 并发 -->
      <div>
        <div class="field-label">
          <span>并发数</span>
          <span class="font-mono text-[11px]" :class="concurrencyWarn ? 'text-amber-300' : 'text-brand-300'">
            {{ concurrency }} / {{ maxConcurrency }}
          </span>
        </div>
        <input
          type="range"
          class="mt-1 w-full"
          :min="1"
          :max="maxConcurrency"
          step="1"
          :value="concurrency"
          :style="rangeStyle"
          :aria-valuetext="`${concurrency} 个并发任务`"
          aria-label="并发数"
          @input="onConcurrencyInput"
        />
        <div class="mt-0.5 flex justify-between px-0.5 font-mono text-[9.5px] text-mute-soft">
          <span v-for="n in maxConcurrency" :key="n" :class="n === concurrency ? 'text-brand-300' : ''">{{ n }}</span>
        </div>
        <p v-if="concurrencyWarn" class="mt-1 flex items-start gap-1 rounded-md bg-amber-500/10 px-2 py-1 text-[10.5px] text-amber-300">
          <Icon name="alert" :size="12" class="mt-0.5 shrink-0" />
          高并发会同时占用多份显存 / 内存，8 个 worker 各自独享一个 session，建议 4 及以下。
        </p>
        <p v-else class="mt-1 text-[10.5px] text-mute-soft">
          每个 worker 独占一个 C++ session（契约要求），并发越高吞吐越大、内存越高。
        </p>
      </div>

      <!-- 高级 -->
      <div class="rounded-lg border border-line-soft bg-ink-900/40">
        <button
          type="button"
          class="flex w-full items-center justify-between px-2.5 py-2 text-[11.5px] text-mute transition-colors hover:text-mute-strong"
          :aria-expanded="advancedOpen"
          @click="advancedOpen = !advancedOpen"
        >
          <span class="flex items-center gap-1.5">
            <Icon name="settings" :size="12" />
            高级：分片 / 线程 / 加速
          </span>
          <Icon name="chevron" :size="13" class="transition-transform duration-150" :class="advancedOpen ? 'rotate-180' : ''" />
        </button>

        <div v-show="advancedOpen" class="space-y-2.5 border-t border-line-soft px-2.5 py-2.5">
          <div>
            <div class="field-label">
              <span>tile 尺寸</span>
              <span class="font-mono text-[10.5px] text-mute-soft">{{ tileSize }}</span>
            </div>
            <div class="mt-1 flex gap-1">
              <button
                v-for="preset in tilePresets"
                :key="preset"
                type="button"
                class="flex-1 rounded border py-0.5 font-mono text-[11px] transition-colors"
                :class="
                  tileSize === preset
                    ? 'border-brand-500/60 bg-brand-500/10 text-brand-200'
                    : 'border-line text-mute hover:border-line-strong'
                "
                @click="setTileSize(String(preset))"
              >
                {{ preset }}
              </button>
            </div>
            <p class="mt-1 text-[10px] text-mute-soft">
              step = tile − overlap = {{ Math.max(1, tileSize - overlap) }}；越大越快，越小越省内存。
            </p>
          </div>

          <div class="grid grid-cols-2 gap-2">
            <label class="block">
              <span class="field-label"><span>overlap</span></span>
              <input
                class="input mt-1 font-mono"
                type="number"
                min="0"
                :max="Math.max(0, tileSize - 1)"
                step="4"
                :value="overlap"
                @input="setOverlap(($event.target as HTMLInputElement).value)"
              />
            </label>
            <label class="block">
              <span class="field-label"><span>线程数</span></span>
              <input
                class="input mt-1 font-mono"
                type="number"
                min="0"
                max="256"
                step="1"
                :value="threads"
                @input="setThreads(($event.target as HTMLInputElement).value)"
              />
            </label>
          </div>
          <p class="-mt-1 text-[10px] text-mute-soft">线程 0 = 按硬件自动（推荐）；overlap 需小于 tile。</p>

          <button
            type="button"
            class="flex w-full items-center justify-between rounded-md border border-line px-2.5 py-1.5 text-[11.5px] text-mute transition-colors hover:border-line-strong"
            @click="toggleFast"
          >
            <span class="flex items-center gap-1.5">
              <Icon name="spark" :size="12" />
              快速模式（略降质量）
            </span>
            <span class="switch" :data-on="fast ? 'true' : 'false'" role="switch" :aria-checked="fast" tabindex="-1">
              <span class="switch-knob" />
            </span>
          </button>
        </div>
      </div>

      <!-- 操作 -->
      <div class="space-y-1.5">
        <button type="button" class="btn-primary w-full !py-2 text-[13px]" :disabled="!canStart" @click="start()">
          <span v-if="starting" class="throbber" />
          <Icon v-else name="play" :size="14" />
          开始处理
          <span v-if="pending > 0" class="font-mono">（{{ pending }}）</span>
        </button>
        <button v-if="running" type="button" class="btn-danger w-full !py-1.5" @click="cancel()">
          <Icon name="stop" :size="13" />
          取消任务
        </button>
        <p v-if="!running && pending === 0" class="text-center text-[10.5px] text-mute-soft">
          先添加图片到队列，Enter 可直接开始
        </p>
        <p v-else-if="running" class="text-center text-[10.5px] text-brand-300">
          任务进行中：队列进度会实时合并到每一行
        </p>
      </div>
    </div>
  </section>
</template>
