import { createApp } from 'vue'
import App from './App.vue'
import './style.css'
import { initTheme } from './lib/theme'
import { initZoom, installZoomShortcuts } from './lib/zoom'

// 启动顺序很重要：先把主题 / 缩放写到 DOM，再挂载 Vue，避免首帧跳一下。
// （index.html 里的内联脚本已先一步恢复 data-theme，这里负责 localStorage 兜底与 color-scheme。）
initTheme()
initZoom()

// 全局缩放快捷键（桌面 Ctrl+=/-/0；浏览器模式 Ctrl+Shift+=/-/0）
installZoomShortcuts()

createApp(App).mount('#app')
