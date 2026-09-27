import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'
import { fileURLToPath, URL } from 'node:url'

const serviceTarget = 'http://127.0.0.1:2048'

// defineConfig defines frontend build and local debugging entry
export default defineConfig({
  plugins: [vue(), tailwindcss()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  build: {
    outDir: fileURLToPath(new URL('../internal/webui/dist', import.meta.url)),
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': serviceTarget,
      '/health': serviceTarget,
      '/v1': serviceTarget,
      '/v1beta': serviceTarget,
    },
  },
})
