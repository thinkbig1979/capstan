import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'path'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    proxy: {
      '/api': {
        target: 'http://localhost:5001',
        changeOrigin: true,
        ws: true,
      },
    },
  },
  build: {
    sourcemap: true,
    rollupOptions: {
      output: {
        // Function form (not the array shorthand) because use-sync-external-store
        // lives only in pnpm's peer-dependency-scoped virtual store path (no root
        // node_modules symlink), which the array shorthand can't resolve as an
        // entry — substring matching on the resolved id works regardless.
        manualChunks(id) {
          if (id.includes('/node_modules/codemirror/') || id.includes('/node_modules/@codemirror/')) {
            return 'codemirror'
          }
          if (id.includes('/node_modules/@xterm/')) {
            return 'xterm'
          }
          if (id.includes('/node_modules/recharts/')) {
            return 'charts'
          }
          // recharts (async-only) pulls in an internal redux stack that itself
          // depends on clsx and use-sync-external-store — both of which are ALSO
          // used eagerly by our own code (clsx via cn()/class-variance-authority,
          // use-sync-external-store via zustand). Left unbucketed, those two land
          // inside the "charts" chunk and force every eager entry that needs them
          // to statically import "charts" too, defeating the lazy boundary.
          // Pinning them to vendor keeps "charts" reachable only via dynamic
          // import() from Sparkline/MetricsPanel. Verified via `pnpm why <pkg>`
          // that the rest of recharts' redux stack (@reduxjs/toolkit, react-redux,
          // reselect, redux-thunk, es-toolkit) has no other consumer in this repo.
          if (
            id.includes('/node_modules/react/') ||
            id.includes('/node_modules/react-dom/') ||
            id.includes('/node_modules/react-router-dom/') ||
            id.includes('/node_modules/clsx/') ||
            id.includes('/node_modules/use-sync-external-store/')
          ) {
            return 'vendor'
          }
        },
      },
    },
  },
})
