import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { resolve } from 'node:path'

// https://vite.dev/config/
//
// 关键配置：
//  - base: '/'，前端部署在根路径（与 Server 的 SPA fallback 配合）
//  - build.outDir: 标准 dist（由 Server/Makefile 的 copy-frontend 搬运到 embed 目录）
//  - dev server 代理 /api -> http://127.0.0.1:8000，使 pnpm dev 能直接调后端接口
export default defineConfig({
  plugins: [vue()],
  base: '/',
  resolve: {
    alias: {
      '@': resolve(__dirname, 'src'),
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8000',
        changeOrigin: true,
      },
    },
  },
})
