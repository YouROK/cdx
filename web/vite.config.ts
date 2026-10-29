import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Сборка кладётся прямо в пакет со встроенной статикой Go.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../internal/webassets/dist',
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: { '/api': { target: 'http://127.0.0.1:7777', changeOrigin: true } },
  },
})
