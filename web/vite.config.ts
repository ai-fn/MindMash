/// <reference types="vitest/config" />
import { defineConfig, loadEnv, type ProxyOptions } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// Local-dev backend port convention. All four Go services share the same
// HTTP_ADDR default (":8080"), so for a simultaneous local run each must be
// started on a distinct port — see web/README.md. These targets are
// overridable via VITE_*_URL env vars; the defaults below are the documented
// convention (identity :8080, collection :8081, scheduler :8082, sync :8083).
function backendProxy(env: Record<string, string>): Record<string, ProxyOptions> {
  const identity = env.VITE_IDENTITY_URL ?? 'http://localhost:8080'
  const collection = env.VITE_COLLECTION_URL ?? 'http://localhost:8081'
  const scheduler = env.VITE_SCHEDULER_URL ?? 'http://localhost:8082'
  const sync = env.VITE_SYNC_URL ?? 'http://localhost:8083'

  // Order matters: Vite matches regex proxy keys in insertion order, so the
  // Scheduler-owned card sub-routes (.../answer, .../preview) and /v1/study
  // MUST come before the generic /v1/cards -> Collection rule, otherwise a
  // POST /v1/cards/{id}/answer would be misrouted to Collection.
  return {
    '^/v1/cards/[^/]+/(answer|preview)$': { target: scheduler, changeOrigin: true },
    '^/v1/study(/|$)': { target: scheduler, changeOrigin: true },
    '^/v1/(auth|users|profiles)(/|$)': { target: identity, changeOrigin: true },
    '^/v1/sync(/|$)': { target: sync, changeOrigin: true },
    // Generic Collection routes last: decks, cards/{id}, notes, note-types.
    '^/v1/(decks|cards|notes|note-types)(/|$)': { target: collection, changeOrigin: true },
  }
}

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), 'VITE_')
  return {
    plugins: [react(), tailwindcss()],
    server: {
      proxy: backendProxy(env),
    },
    test: {
      environment: 'jsdom',
      globals: true,
      setupFiles: ['./src/test/setup.ts'],
      css: false,
    },
  }
})
