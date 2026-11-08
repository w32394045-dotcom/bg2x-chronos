import { writeFileSync, existsSync } from 'node:fs'
import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

const distDir = fileURLToPath(new URL('./dist', import.meta.url))

/**
 * dist/ 归 Wails embed 使用，构建前会被清空；.gitkeep 必须保留在仓库里，
 * 否则 `git clean` 会让之后所有构建的 dist 目录丢失占位文件。
 */
function preserveGitkeep() {
  return {
    name: 'chronos-preserve-gitkeep',
    closeBundle() {
      const target = fileURLToPath(new URL('./dist/.gitkeep', import.meta.url))
      if (!existsSync(target)) writeFileSync(target, '')
    },
  }
}

export default defineConfig({
  // Wails 用 asset server 嵌入 dist，必须以相对路径引用 assets
  base: './',
  plugins: [vue(), preserveGitkeep()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  build: {
    outDir: distDir,
    emptyOutDir: true,
    target: 'es2020',
    assetsInlineLimit: 4096,
    chunkSizeWarningLimit: 700,
    reportCompressedSize: true,
  },
  server: {
    port: 5173,
    strictPort: false,
  },
  test: {
    environment: 'node',
    include: ['src/**/__tests__/**/*.test.ts'],
    reporters: 'default',
  },
})
