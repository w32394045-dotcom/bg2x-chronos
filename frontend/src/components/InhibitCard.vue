<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import Icon from './Icon.vue'
import type { ChronosStore } from '@/composables/useChronos'
import { backendLabel } from '@/lib/labels'
import { formatDuration } from '@/lib/format'

const props = defineProps<{ store: ChronosStore }>()
const { appInfo, inhibitRequest, inhibitStatus, updateInhibit, toast, refreshInhibit } = props.store

type SwitchKey = 'sleep' | 'display' | 'lock'

interface InhibitSwitch {
  key: SwitchKey
  label: string
  desc: string
  icon: string
  supportedKey: 'sleepSupported' | 'displaySupported' | 'lockSupported'
}

const SWITCHES: InhibitSwitch[] = [
  { key: 'sleep', label: '阻止睡眠', desc: '系统空闲时不进入睡眠', icon: 'clock', supportedKey: 'sleepSupported' },
  { key: 'display', label: '阻止熄屏', desc: '保持显示器常亮', icon: 'monitor', supportedKey: 'displaySupported' },
  { key: 'lock', label: '阻止空闲锁屏', desc: '空闲时不被自动锁定', icon: 'lock', supportedKey: 'lockSupported' },
]

const nowMs = ref(Date.now())
let ticker: ReturnType<typeof setInterval> | null = null

const active = computed(() => inhibitStatus.value.active)
const sinceText = computed(() => {
  if (!active.value || inhibitStatus.value.sinceMs <= 0) return '未激活'
  return `已保持 ${formatDuration(Math.max(0, nowMs.value - inhibitStatus.value.sinceMs))}`
})
const anySupported = computed(
  () =>
    inhibitStatus.value.sleepSupported ||
    inhibitStatus.value.displaySupported ||
    inhibitStatus.value.lockSupported,
)
const unsupported = computed(() => !anySupported.value || inhibitStatus.value.driver === 'unsupported')

const caveat = computed(() => {
  const platform = appInfo.value?.platform ?? ''
  if (platform.startsWith('windows')) {
    return 'Windows：调用 SetThreadExecutionState 阻止系统空闲睡眠 / 熄屏 / 空闲锁屏；手动 Win+L 无法被阻止。'
  }
  if (platform.startsWith('linux')) {
    return 'Linux：通过 logind（org.freedesktop.login1）的 Inhibit 与 org.freedesktop.ScreenSaver D-Bus 接口保持唤醒，需要桌面会话支持。'
  }
  if (platform.startsWith('darwin')) {
    return 'macOS：尚未实现电源保持驱动，开关会显示为不可用，仅提供状态查询。'
  }
  if (platform.startsWith('browser')) {
    return '浏览器预览：这里只模拟状态切换；桌面端才会真正调用系统电源 API。'
  }
  return '当前平台暂不支持电源保持：只提供状态查询，开关不会生效。'
})

function isSupported(item: InhibitSwitch): boolean {
  return inhibitStatus.value[item.supportedKey]
}

function toggle(item: InhibitSwitch): void {
  if (!isSupported(item)) {
    toast('warn', `${item.label}：当前平台不支持（${backendLabel(inhibitStatus.value.driver)}）`)
    return
  }
  void updateInhibit({ [item.key]: !inhibitRequest[item.key] })
}

function toggleAutoRelease(): void {
  void updateInhibit({ autoRelease: !inhibitRequest.autoRelease })
}

onMounted(() => {
  ticker = setInterval(() => {
    nowMs.value = Date.now()
  }, 1000)
  void refreshInhibit()
})

onUnmounted(() => {
  if (ticker) clearInterval(ticker)
  ticker = null
})
</script>

<template>
  <section class="panel">
    <div class="panel-head">
      <div class="panel-title">
        <Icon name="lock" :size="14" class="text-mute" />
        电源保持
        <span
          class="chip"
          :class="
            active
              ? 'bg-emerald-500/10 text-emerald-300 ring-emerald-400/25'
              : 'bg-white/5 text-mute-soft ring-white/10'
          "
        >
          <span
            class="h-1.5 w-1.5 rounded-full"
            :class="active ? 'animate-pulse-soft bg-emerald-300' : 'bg-mute-soft'"
          />
          {{ active ? '生效中' : '未激活' }}
        </span>
      </div>
      <span class="font-mono text-[10px] text-mute-soft">{{ sinceText }}</span>
    </div>

    <div class="panel-body space-y-2">
      <button
        v-for="item in SWITCHES"
        :key="item.key"
        type="button"
        class="flex w-full items-center gap-2 rounded-lg border px-2.5 py-1.5 text-left transition-colors"
        :class="
          isSupported(item)
            ? 'border-line bg-ink-900/40 hover:border-line-strong'
            : 'cursor-not-allowed border-line-soft bg-ink-900/20 opacity-60'
        "
        :aria-pressed="inhibitRequest[item.key]"
        :aria-disabled="!isSupported(item)"
        :title="isSupported(item) ? item.desc : '当前平台不支持'"
        @click="toggle(item)"
      >
        <Icon :name="item.icon" :size="14" class="shrink-0 text-mute" />
        <span class="min-w-0 flex-1">
          <span class="block truncate text-[12px] text-mute-strong">{{ item.label }}</span>
          <span class="block truncate text-[10px] text-mute-soft">{{ item.desc }}</span>
        </span>
        <span class="switch" :data-on="inhibitRequest[item.key] ? 'true' : 'false'" role="switch" :aria-checked="inhibitRequest[item.key]">
          <span class="switch-knob" />
        </span>
      </button>

      <button
        type="button"
        class="flex w-full items-center gap-2 rounded-lg border border-line bg-ink-900/40 px-2.5 py-1.5 text-left transition-colors hover:border-line-strong"
        :aria-pressed="inhibitRequest.autoRelease"
        @click="toggleAutoRelease"
      >
        <Icon name="refresh" :size="14" class="shrink-0 text-mute" />
        <span class="min-w-0 flex-1">
          <span class="block truncate text-[12px] text-mute-strong">任务结束自动释放</span>
          <span class="block truncate text-[10px] text-mute-soft">BatchRequest.Inhibit.AutoRelease</span>
        </span>
        <span class="switch" :data-on="inhibitRequest.autoRelease ? 'true' : 'false'" role="switch" :aria-checked="inhibitRequest.autoRelease">
          <span class="switch-knob" />
        </span>
      </button>

      <div class="rounded-lg border border-line-soft bg-ink-900/40 px-2.5 py-2 text-[10.5px] text-mute-soft">
        <div class="flex items-center gap-1.5">
          <Icon name="cpu" :size="11" />
          <span class="font-mono text-mute">{{ backendLabel(inhibitStatus.driver) }}</span>
          <span v-if="inhibitStatus.detail" class="truncate" :title="inhibitStatus.detail">· {{ inhibitStatus.detail }}</span>
        </div>
        <p class="mt-1 leading-relaxed">{{ caveat }}</p>
        <p v-if="unsupported" class="mt-1 flex items-start gap-1 rounded bg-amber-500/10 px-1.5 py-1 text-amber-300">
          <Icon name="alert" :size="11" class="mt-0.5 shrink-0" />
          当前平台不支持电源保持（driver=unsupported），三项开关均不可用。
        </p>
      </div>
    </div>
  </section>
</template>
