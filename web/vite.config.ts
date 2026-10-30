import { writeFileSync } from 'node:fs'
import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

// In dev the backend listens on :8080 and its public URL is http://localhost:5173,
// so the browser's Origin header must reach it untouched (changeOrigin stays off).
const backend = 'http://localhost:8080'
const proxied = ['/api', '/mcp', '/u', '/healthz', '/docs']

export default defineConfig({
  plugins: [
    vue(),
    tailwindcss(),
    // emptyOutDir wipes dist/.gitkeep, which web/embed.go needs so `go build`
    // works in a clean checkout; put it back after every build.
    {
      name: 'keep-dist-gitkeep',
      apply: 'build',
      closeBundle() {
        writeFileSync(fileURLToPath(new URL('./dist/.gitkeep', import.meta.url)), '')
      },
    },
  ],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 900,
  },
  server: {
    port: 5173,
    proxy: Object.fromEntries(proxied.map((p) => [p, { target: backend, changeOrigin: false }])),
  },
})
