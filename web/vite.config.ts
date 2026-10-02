import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Built into the Go binary: internal/web/dist is embedded at compile time.
export default defineConfig({
  plugins: [react()],
  build: { outDir: '../internal/web/dist', emptyOutDir: true, chunkSizeWarningLimit: 900 },
  server: { proxy: { '/api': { target: 'http://127.0.0.1:8080', changeOrigin: false } } },
})
