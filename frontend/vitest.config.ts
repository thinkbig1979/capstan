import { defineConfig } from 'vitest/config'
import { searchForWorkspaceRoot } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(import.meta.dirname, './src'),
    },
  },
  // The help loader globs ../docs (src/help/docs-raw.ts). This config does not
  // merge vite.config.ts, and vitest enforces fs.allow on the glob's imports:
  // without ../docs here they fail with "Denied ID .../docs/...md?raw".
  server: {
    fs: { allow: [searchForWorkspaceRoot(process.cwd()), path.resolve(import.meta.dirname, '../docs')] },
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: './src/test/setup.ts',
  },
})
