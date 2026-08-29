import { defineConfig } from 'vite'

export default defineConfig({
  build: { outDir: '../cmd/server/web/dist' },
  server: { proxy: { '/api': 'http://localhost:8080', '/login': 'http://localhost:8080' } },
})
