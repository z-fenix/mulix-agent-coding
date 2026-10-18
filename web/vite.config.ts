// vite.config.ts——web 构建管线：react 插件 + 产物直写 go:embed 目录（design §8）。
// build.outDir 指向 internal/serve/dist（emptyOutDir 由 Task 3 的 embed 占位纪律保护：
// 构建后需 `git checkout -- internal/serve/dist/index.html` 恢复占位再提交）。
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../internal/serve/dist',
    emptyOutDir: true,
  },
  test: {
    environment: 'jsdom',
    globals: true,
  },
})
