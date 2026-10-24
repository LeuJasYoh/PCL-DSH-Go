import { fileURLToPath, URL } from 'node:url'

import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

// Development proxy keeps the browser on one origin so the Spring session
// cookie and the XSRF-TOKEN cookie are shared with the SPA (see plan 3.4/4.4).
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    host: '127.0.0.1',
    port: 5173,
    strictPort: false,
    // E2E data lives inside the project; watching it on Windows keeps directory
    // handles that make backend trash moves (rename) fail with a sharing violation.
    watch: {
      ignored: ['**/.e2e-data/**', '**/test-results/**', '**/playwright-report/**'],
    },
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: false,
      },
      '/workbench': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: false,
        ws: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    assetsInlineLimit: 4096,
    chunkSizeWarningLimit: 900,
  },
})
