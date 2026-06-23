import { defineConfig } from '@playwright/test'

// Finalized in Task 9 (see task-9-brief.md): the Vite dev server is started/reused
// automatically via `webServer` so `npm run e2e` is a single command. The four Go backend
// services are a separate multi-process stack this config intentionally does NOT start —
// see web/README.md's "End-to-end tests" section for how to start them manually first.
export default defineConfig({
  testDir: './e2e',
  use: {
    baseURL: 'http://localhost:5173',
  },
  webServer: {
    command: 'npm run dev',
    url: 'http://localhost:5173',
    reuseExistingServer: !process.env.CI,
  },
})
