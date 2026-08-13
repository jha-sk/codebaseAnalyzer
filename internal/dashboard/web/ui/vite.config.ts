/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  // The Go binary embeds ../dist, so that is where the build must land.
  build: { outDir: '../dist', emptyOutDir: true },
  // Relative asset URLs keep the bundle servable from any mount point.
  base: './',
  // `npm run dev` talks to a locally running dashboard binary.
  server: { proxy: { '/api': 'http://localhost:8080' } },
  test: { environment: 'jsdom', globals: true, setupFiles: './src/test/setup.ts' },
})
