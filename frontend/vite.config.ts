import {defineConfig} from 'vitest/config'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {host: '127.0.0.1', port: 5173, strictPort: true, proxy: {'/api': 'http://127.0.0.1:8088', '/auth': 'http://127.0.0.1:8088'}},
  build: {outDir: 'dist', emptyOutDir: true},
  test: {environment: 'jsdom', setupFiles: './src/test/setup.ts', maxWorkers: 4}
})
