import { mkdirSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig, type Plugin } from 'vite'

const backend = process.env.LANEPOOL_API ?? 'http://localhost:8000'

// Go embeds web/dist with //go:embed all:dist, so the directory (and a tracked
// placeholder) must survive every build, which empties dist/ first.
function keepDistPlaceholder(): Plugin {
  return {
    name: 'lanepool-keep-dist-gitkeep',
    apply: 'build',
    closeBundle() {
      const dist = path.resolve(import.meta.dirname, 'dist')
      mkdirSync(dist, { recursive: true })
      writeFileSync(path.join(dist, '.gitkeep'), '')
    },
  }
}

export default defineConfig({
  base: '/',
  plugins: [react(), tailwindcss(), keepDistPlaceholder()],
  resolve: {
    alias: { '@': path.resolve(import.meta.dirname, './src') },
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': { target: backend, changeOrigin: false },
      '/healthz': { target: backend, changeOrigin: false },
      '/readyz': { target: backend, changeOrigin: false },
      '/metrics': { target: backend, changeOrigin: false },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 1500,
  },
})
