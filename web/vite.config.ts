import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// In dev, the Go backend (fake platform) runs on :8532 and Vite proxies /api
// to it — SSE included (plain HTTP streaming, no ws upgrade).
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/api': {
        target: 'http://localhost:8532',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: false,
  },
})
