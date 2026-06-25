import { beforeEach, describe, expect, it, vi } from 'vitest'

// Modules are dynamically imported fresh per test (vi.resetModules) so the store's
// module-level singleton (created at import time, reading localStorage) never leaks
// state across tests — same rationale as themeStore.test.ts.
async function loadStore() {
  return import('./studyPrefsStore')
}

describe('studyPrefsStore', () => {
  beforeEach(() => {
    vi.resetModules()
    window.localStorage.clear()
  })

  describe('initial resolution', () => {
    it('defaults to advancedGrading: false with no persisted preference', async () => {
      const { useStudyPrefsStore } = await loadStore()

      expect(useStudyPrefsStore.getState().advancedGrading).toBe(false)
    })

    it('reads a persisted "true" value', async () => {
      window.localStorage.setItem('mindmesh.study.advancedGrading', 'true')
      const { useStudyPrefsStore } = await loadStore()

      expect(useStudyPrefsStore.getState().advancedGrading).toBe(true)
    })

    it('treats any non-"true" persisted value as false', async () => {
      window.localStorage.setItem('mindmesh.study.advancedGrading', 'garbage')
      const { useStudyPrefsStore } = await loadStore()

      expect(useStudyPrefsStore.getState().advancedGrading).toBe(false)
    })
  })

  describe('setAdvancedGrading', () => {
    it('updates state and persists to localStorage', async () => {
      const { useStudyPrefsStore } = await loadStore()

      useStudyPrefsStore.getState().setAdvancedGrading(true)

      expect(useStudyPrefsStore.getState().advancedGrading).toBe(true)
      expect(window.localStorage.getItem('mindmesh.study.advancedGrading')).toBe('true')
    })
  })

  describe('toggle', () => {
    it('flips advancedGrading and persists the new value', async () => {
      const { useStudyPrefsStore } = await loadStore()

      useStudyPrefsStore.getState().toggle()
      expect(useStudyPrefsStore.getState().advancedGrading).toBe(true)
      expect(window.localStorage.getItem('mindmesh.study.advancedGrading')).toBe('true')

      useStudyPrefsStore.getState().toggle()
      expect(useStudyPrefsStore.getState().advancedGrading).toBe(false)
      expect(window.localStorage.getItem('mindmesh.study.advancedGrading')).toBe('false')
    })
  })
})
