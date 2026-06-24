import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// matchMedia listeners registered by the module are captured here so individual tests
// can simulate an OS-level prefers-color-scheme change by invoking the captured handler.
type MediaListener = () => void

function installMatchMediaMock(initialPrefersDark: boolean) {
  let prefersDark = initialPrefersDark
  const listeners: MediaListener[] = []

  const mql = {
    get matches() {
      return prefersDark
    },
    media: '(prefers-color-scheme: dark)',
    addEventListener: vi.fn((_event: string, listener: MediaListener) => {
      listeners.push(listener)
    }),
    removeEventListener: vi.fn(),
  }

  vi.stubGlobal(
    'matchMedia',
    vi.fn(() => mql),
  )

  return {
    setPrefersDark(value: boolean) {
      prefersDark = value
      listeners.forEach((listener) => listener())
    },
  }
}

// Modules are dynamically imported fresh per test (vi.resetModules) because the store's
// module-level singleton (created at import time, including its matchMedia 'change'
// listener registration) must not leak state or registered listeners across tests —
// same rationale as authStore.test.ts.
async function loadStore() {
  return import('./themeStore')
}

describe('themeStore', () => {
  beforeEach(() => {
    vi.resetModules()
    window.localStorage.clear()
    document.documentElement.removeAttribute('data-theme')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  describe('initial resolution', () => {
    it('defaults to system mode with no persisted preference, resolving to light when OS prefers light', async () => {
      installMatchMediaMock(false)
      const { useThemeStore } = await loadStore()

      expect(useThemeStore.getState().mode).toBe('system')
      expect(useThemeStore.getState().effective).toBe('light')
      expect(document.documentElement.getAttribute('data-theme')).toBe('light')
    })

    it('resolves system mode to dark when the OS prefers dark', async () => {
      installMatchMediaMock(true)
      const { useThemeStore } = await loadStore()

      expect(useThemeStore.getState().effective).toBe('dark')
      expect(document.documentElement.getAttribute('data-theme')).toBe('dark')
    })

    it('reads a persisted explicit mode over the system default', async () => {
      installMatchMediaMock(true)
      window.localStorage.setItem('mindmesh.theme', 'light')
      const { useThemeStore } = await loadStore()

      expect(useThemeStore.getState().mode).toBe('light')
      expect(useThemeStore.getState().effective).toBe('light')
      expect(document.documentElement.getAttribute('data-theme')).toBe('light')
    })

    it('ignores an invalid persisted value and falls back to system', async () => {
      installMatchMediaMock(false)
      window.localStorage.setItem('mindmesh.theme', 'neon')
      const { useThemeStore } = await loadStore()

      expect(useThemeStore.getState().mode).toBe('system')
    })
  })

  describe('setMode', () => {
    it('applies the data-theme attribute and persists the mode to localStorage', async () => {
      installMatchMediaMock(false)
      const { useThemeStore } = await loadStore()

      useThemeStore.getState().setMode('dark')

      expect(useThemeStore.getState().mode).toBe('dark')
      expect(useThemeStore.getState().effective).toBe('dark')
      expect(document.documentElement.getAttribute('data-theme')).toBe('dark')
      expect(window.localStorage.getItem('mindmesh.theme')).toBe('dark')
    })

    it('re-resolves against the current OS preference when switching to system', async () => {
      installMatchMediaMock(true)
      const { useThemeStore } = await loadStore()

      useThemeStore.getState().setMode('light')
      expect(useThemeStore.getState().effective).toBe('light')

      useThemeStore.getState().setMode('system')

      expect(useThemeStore.getState().mode).toBe('system')
      expect(useThemeStore.getState().effective).toBe('dark')
      expect(document.documentElement.getAttribute('data-theme')).toBe('dark')
    })
  })

  describe('OS preference changes while in system mode', () => {
    it('updates effective and the DOM attribute live when prefers-color-scheme changes', async () => {
      const media = installMatchMediaMock(false)
      const { useThemeStore } = await loadStore()
      expect(useThemeStore.getState().effective).toBe('light')

      media.setPrefersDark(true)

      expect(useThemeStore.getState().effective).toBe('dark')
      expect(document.documentElement.getAttribute('data-theme')).toBe('dark')
    })

    it('does not react to OS changes when an explicit mode is set', async () => {
      const media = installMatchMediaMock(false)
      const { useThemeStore } = await loadStore()
      useThemeStore.getState().setMode('light')

      media.setPrefersDark(true)

      expect(useThemeStore.getState().mode).toBe('light')
      expect(useThemeStore.getState().effective).toBe('light')
      expect(document.documentElement.getAttribute('data-theme')).toBe('light')
    })
  })

  describe('cycle', () => {
    it('cycles light -> dark -> system -> light', async () => {
      installMatchMediaMock(false)
      const { useThemeStore } = await loadStore()
      useThemeStore.getState().setMode('light')

      useThemeStore.getState().cycle()
      expect(useThemeStore.getState().mode).toBe('dark')

      useThemeStore.getState().cycle()
      expect(useThemeStore.getState().mode).toBe('system')

      useThemeStore.getState().cycle()
      expect(useThemeStore.getState().mode).toBe('light')
    })
  })
})
