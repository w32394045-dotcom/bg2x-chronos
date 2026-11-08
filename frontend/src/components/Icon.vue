<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    name: string
    size?: number
    strokeWidth?: number
  }>(),
  { size: 16, strokeWidth: 1.7 },
)

const PATHS: Record<string, string> = {
  plus: 'M12 5v14M5 12h14',
  minus: 'M5 12h14',
  sun: 'M12 8.5a3.5 3.5 0 1 0 0 7 3.5 3.5 0 0 0 0-7zM12 3v2M12 19v2M3 12h2M19 12h2M5.6 5.6l1.4 1.4M17 17l1.4 1.4M18.4 5.6L17 7M7 17l-1.4 1.4',
  moon: 'M20 14.5A8.5 8.5 0 0 1 9.5 4a8.5 8.5 0 1 0 10.5 10.5z',
  folder:
    'M3 7.5A1.5 1.5 0 0 1 4.5 6h4l2 2.5h7A1.5 1.5 0 0 1 19 10v7.5a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 3 17.5z',
  play: 'M8 5.4v13.2L19 12z',
  stop: 'M7 7h10v10H7z',
  refresh: 'M20 11a8 8 0 1 0-2.35 5.65M20 5v6h-6',
  settings:
    'M12 15.4a3.4 3.4 0 1 0 0-6.8 3.4 3.4 0 0 0 0 6.8zM19.1 15a1.6 1.6 0 0 0 .32 1.77l.06.06a1.9 1.9 0 1 1-2.69 2.69l-.06-.06a1.6 1.6 0 0 0-1.77-.32 1.6 1.6 0 0 0-.97 1.47V21a1.9 1.9 0 1 1-3.8 0v-.1a1.6 1.6 0 0 0-1.05-1.47 1.6 1.6 0 0 0-1.77.32l-.06.06a1.9 1.9 0 1 1-2.69-2.69l.06-.06A1.6 1.6 0 0 0 4.98 15a1.6 1.6 0 0 0-1.47-.97H3.4a1.9 1.9 0 1 1 0-3.8h.1a1.6 1.6 0 0 0 1.47-1.05 1.6 1.6 0 0 0-.32-1.77l-.06-.06a1.9 1.9 0 1 1 2.69-2.69l.06.06a1.6 1.6 0 0 0 1.77.32h.08A1.6 1.6 0 0 0 10.2 3.6V3.4a1.9 1.9 0 1 1 3.8 0v.1a1.6 1.6 0 0 0 .97 1.47 1.6 1.6 0 0 0 1.77-.32l.06-.06a1.9 1.9 0 1 1 2.69 2.69l-.06.06a1.6 1.6 0 0 0-.32 1.77v.08a1.6 1.6 0 0 0 1.47.97h.1a1.9 1.9 0 1 1 0 3.8h-.1a1.6 1.6 0 0 0-1.47.97z',
  close: 'M6 6l12 12M18 6L6 18',
  external: 'M14 4h6v6M20 4l-8 8M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5',
  chevron: 'M6 9l6 6 6-6',
  trash: 'M4 7h16M9 7V5a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2M6 7l1 13h10l1-13M10 11v6M14 11v6',
  alert: 'M12 3.6 21 20H3zM12 9.2v5M12 17.4h.01',
  check: 'M9 12.4l2.3 2.3 4.7-5.4M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0z',
  image:
    'M4 5.6A1.6 1.6 0 0 1 5.6 4h12.8A1.6 1.6 0 0 1 20 5.6v12.8a1.6 1.6 0 0 1-1.6 1.6H5.6A1.6 1.6 0 0 1 4 18.4zM8.6 10a1.6 1.6 0 1 0 0-3.2 1.6 1.6 0 0 0 0 3.2zM4 16.4l4.6-4.1 3.9 3.4 2.9-2.4 4.6 4.1',
  cpu: 'M8 8h8v8H8zM4.5 9.5H3M4.5 14.5H3M21 9.5h-1.5M21 14.5h-1.5M9.5 4.5V3M14.5 4.5V3M9.5 21v-1.5M14.5 21v-1.5',
  monitor:
    'M4 6.6A1.6 1.6 0 0 1 5.6 5h12.8A1.6 1.6 0 0 1 20 6.6v8.8a1.6 1.6 0 0 1-1.6 1.6H5.6A1.6 1.6 0 0 1 4 15.4zM9 20h6M12 17v3',
  lock: 'M7 11V8a5 5 0 0 1 10 0v3M6 11h12v8.4a1.6 1.6 0 0 1-1.6 1.6H7.6A1.6 1.6 0 0 1 6 19.4z',
  layers: 'M12 3l9 5-9 5-9-5zM3 13l9 5 9-5M3 17l9 4.5 9-4.5',
  clock: 'M12 7v5l3.4 2M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0z',
  info: 'M12 11v6M12 7.6h.01M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0z',
  'x-circle': 'M9 9l6 6M15 9l-6 6M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0z',
  compare: 'M12 4v16M8 8l-4 4 4 4M16 8l4 4-4 4',
  spark: 'M12 4l1.7 4.6L18 10l-4.3 1.4L12 16l-1.7-4.6L6 10l4.3-1.4zM18.4 15.4l.8 2.2 2.2.8-2.2.8-.8 2.2-.8-2.2-2.2-.8 2.2-.8z',
  download: 'M12 4v11M7.5 10.5 12 15l4.5-4.5M5 19h14',
  grid: 'M4 4h7v7H4zM13 4h7v7h-7zM4 13h7v7H4zM13 13h7v7h-7z',
}

const path = computed(() => PATHS[props.name] ?? PATHS.info)
</script>

<template>
  <svg
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    fill="none"
    :stroke-width="strokeWidth"
    stroke="currentColor"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
    focusable="false"
  >
    <path :d="path" />
  </svg>
</template>
