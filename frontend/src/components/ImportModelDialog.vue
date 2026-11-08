<script setup lang="ts">
/**
 * 外部模型导入对话框（契约 v3 ImportModel）。
 *
 * 桌面模式用原生文件选择；浏览器模式没有绝对路径，允许直接粘贴路径（也支持
 * 目录，里面放 .onnx + .onnx.data）。导入后可立刻跑一次自检。
 */
import { computed, ref } from 'vue'
import Icon from './Icon.vue'
import { api, isBrowserMode } from '@/lib/api'
import type { ModelInfo, ModelSelfTest } from '@/lib/types'

const emit = defineEmits<{
  (event: 'close'): void
  (event: 'imported', model: ModelInfo): void
}>()

const path = ref('')
const name = ref('')
const autoDetect = ref(true)
const busy = ref(false)
const error = ref('')
const imported = ref<ModelInfo | null>(null)
const test = ref<ModelSelfTest | null>(null)
const testing = ref(false)

const browser = computed(() => isBrowserMode())
const canImport = computed(() => path.value.trim() !== '' && !busy.value)

async function browse(): Promise<void> {
  const picked = await api.PickModelFile()
  if (picked) path.value = picked
}

async function runImport(): Promise<void> {
  error.value = ''
  test.value = null
  imported.value = null
  busy.value = true
  try {
    const info = await api.ImportModel({
      path: path.value.trim(),
      name: name.value.trim(),
      autoDetect: autoDetect.value,
    })
    imported.value = info
    emit('imported', info)
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    busy.value = false
  }
}

async function runSelfTest(): Promise<void> {
  if (!imported.value) return
  testing.value = true
  try {
    test.value = await api.SelfTestModel(imported.value.id)
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    testing.value = false
  }
}
</script>

<template>
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-scrim/70 p-6 backdrop-blur-sm">
    <div class="panel w-full max-w-[520px]">
      <div class="panel-head">
        <div class="panel-title">
          <Icon name="download" :size="14" class="text-mute" />
          导入外部模型
        </div>
        <button type="button" class="icon-btn" aria-label="关闭" @click="emit('close')">
          <Icon name="close" :size="14" />
        </button>
      </div>

      <div class="panel-body space-y-3">
        <div>
          <label class="field-label">
            <span>模型路径</span>
            <span class="font-mono text-[10px] text-mute-soft">.onnx 或含 .onnx/.data 的目录</span>
          </label>
          <div class="mt-1 flex gap-1.5">
            <input
              v-model="path"
              class="input font-mono"
              type="text"
              placeholder="C:\models\RealESRGAN_x4plus.onnx"
              spellcheck="false"
            />
            <button v-if="!browser" type="button" class="btn-ghost shrink-0" @click="browse">选择…</button>
          </div>
        </div>

        <div>
          <label class="field-label">
            <span>显示名（可选）</span>
          </label>
          <input v-model="name" class="input mt-1" type="text" placeholder="例如 4x-UltraSharp" />
        </div>

        <label class="flex items-center gap-2 text-[11.5px] text-mute">
          <input v-model="autoDetect" type="checkbox" class="h-3.5 w-3.5 accent-brand-500" />
          自动探测倍率/输入输出名（需要本机 ONNX Runtime；失败时按 x4 处理并写入提示）
        </label>

        <p v-if="error" class="rounded-md bg-rose-500/10 px-2 py-1 text-[11px] text-rose-300">
          {{ error }}
        </p>

        <div v-if="imported" class="rounded-lg border border-emerald-400/30 bg-emerald-500/[0.08] px-2.5 py-2">
          <div class="flex items-center gap-1.5 text-[12px] text-emerald-300">
            已导入 {{ imported.name }}
            <span class="chip bg-emerald-500/10 text-emerald-300 ring-emerald-400/25">x{{ imported.scale }}</span>
          </div>
          <p class="mt-0.5 break-all text-[10.5px] text-mute-soft">{{ imported.path }}</p>
          <button type="button" class="btn-ghost mt-1.5" :disabled="testing" @click="runSelfTest">
            {{ testing ? '自检中…' : '立即自检（加载/尺寸/接缝/色彩）' }}
          </button>
          <div v-if="test" class="mt-1.5 space-y-0.5 text-[10.5px]">
            <div v-for="check in test.checks" :key="check.name" :class="check.ok ? 'text-emerald-300' : 'text-amber-300'">
              {{ check.ok ? '✓' : '✗' }} {{ check.name }} — {{ check.detail }}
            </div>
          </div>
        </div>

        <p class="text-[10.5px] leading-relaxed text-mute-soft">
          支持范围：1 个输入、1 个输出、RGB、NCHW、倍数 1–8 的超分/修复类 ONNX。
          多输出会取第一个 4D 输出；int8 量化模型不支持。
        </p>
      </div>

      <div class="flex items-center justify-end gap-2 border-t border-line-soft px-3.5 py-2.5">
        <button type="button" class="btn-ghost" @click="emit('close')">关闭</button>
        <button type="button" class="btn-primary" :disabled="!canImport" @click="runImport">
          {{ busy ? '导入中…' : '导入' }}
        </button>
      </div>
    </div>
  </div>
</template>
