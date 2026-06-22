import { defineConfig } from '@playwright/test'

// Minimal wiring for Task 1 — no specs yet (added in Task 9), no webServer yet
// (the E2E task documents how to start the backend + `npm run dev` manually).
export default defineConfig({
  testDir: './e2e',
  use: {
    baseURL: 'http://localhost:5173',
  },
})
