import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// 部署在 Go 后端的 /app/ 下；base 让产物里的资源引用带上前缀。
export default defineConfig({
  base: '/app/',
  plugins: [react()],
  build: { outDir: 'dist', emptyOutDir: true },
  server: { port: 5173 },
})
