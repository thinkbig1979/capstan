import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'path'

export default defineConfig(({ mode }) => ({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(import.meta.dirname, './src'),
    },
  },
  server: {
    // This also serves `vite preview`: vite resolves the preview proxy as
    // `preview?.proxy ?? server.proxy`, so adding a `preview.proxy` block here
    // would be a no-op duplicate, and a second place for the target below to
    // drift out of sync. Verified on vite 8.2.1 by proxying a live request
    // through `vite preview` against a config with no `preview` key. What
    // usually gets misread as a missing preview proxy is the real trap:
    // `vite preview` must be run from `frontend/`, or no vite.config.ts is
    // loaded at all and /api falls through to index.html via the SPA fallback.
    proxy: {
      '/api': {
        target: 'http://localhost:5001',
        changeOrigin: true,
        ws: true,
      },
    },
  },
  build: {
    // Off for production: source maps reconstruct the original TypeScript —
    // component structure, comments, internal API call shapes — for anyone who
    // can load the app, and they cost roughly 8MB in every image layer and
    // registry pull for files no production user ever requests. A local
    // `vite build --mode development` still gets them, and the dev server
    // serves maps regardless of this setting, so nothing is lost while
    // developing. Switch to 'hidden' if an error tracker is ever added — but
    // then also narrow the dist COPY in docker/Dockerfile, or the maps ship
    // anyway and this is undone.
    sourcemap: mode !== 'production',
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
            id.includes('/node_modules/react-router/') ||
            id.includes('/node_modules/clsx/') ||
            id.includes('/node_modules/use-sync-external-store/')
          ) {
            return 'vendor'
          }
        },
      },
    },
  },
}))
