import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

export default defineConfig({
  plugins: [svelte()],
  build: { outDir: '../cmd/server/web/dist' },
  server: { proxy: { '/api': 'http://localhost:8080', '/login': 'http://localhost:8080' } },
})
