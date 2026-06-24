// Zustand theme store: the app's single source of truth for the user's display-theme
// preference (`mode`). `mode` is the user's stated preference and is persisted to
// localStorage; `effective` is the resolved 'light' | 'dark' actually applied to the DOM
// (mode 'system' resolves via `prefers-color-scheme`). The store applies `data-theme` to
// `document.documentElement` as a side effect of every mode/effective change so that
// `src/index.css`'s `[data-theme="light"|"dark"]` selectors take effect — no React
// component/provider is required to "render" the theme, which keeps the very first paint
// (before React hydrates) able to read the same localStorage key directly if needed later.
import { create } from 'zustand'

export type ThemeMode = 'light' | 'dark' | 'system'
type EffectiveTheme = 'light' | 'dark'

const STORAGE_KEY = 'mindmesh.theme'
const MODES: ThemeMode[] = ['light', 'dark', 'system']

interface ThemeState {
  mode: ThemeMode
  effective: EffectiveTheme
  setMode: (mode: ThemeMode) => void
  cycle: () => void
}

function prefersDark(): boolean {
  // jsdom (the test environment) does not implement matchMedia by default; guard so
  // store creation never throws outside a real browser/mocked environment.
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
    return false
  }
  return window.matchMedia('(prefers-color-scheme: dark)').matches
}

function resolveEffective(mode: ThemeMode): EffectiveTheme {
  return mode === 'system' ? (prefersDark() ? 'dark' : 'light') : mode
}

function readPersistedMode(): ThemeMode {
  if (typeof window === 'undefined' || !window.localStorage) {
    return 'system'
  }
  const stored = window.localStorage.getItem(STORAGE_KEY)
  return stored === 'light' || stored === 'dark' || stored === 'system' ? stored : 'system'
}

function persistMode(mode: ThemeMode): void {
  if (typeof window === 'undefined' || !window.localStorage) {
    return
  }
  window.localStorage.setItem(STORAGE_KEY, mode)
}

function applyEffectiveToDom(effective: EffectiveTheme): void {
  if (typeof document === 'undefined') {
    return
  }
  document.documentElement.setAttribute('data-theme', effective)
}

export const useThemeStore = create<ThemeState>((set, get) => {
  const initialMode = readPersistedMode()
  const initialEffective = resolveEffective(initialMode)
  applyEffectiveToDom(initialEffective)

  return {
    mode: initialMode,
    effective: initialEffective,

    setMode(mode: ThemeMode) {
      const effective = resolveEffective(mode)
      persistMode(mode)
      applyEffectiveToDom(effective)
      set({ mode, effective })
    },

    cycle() {
      const currentIndex = MODES.indexOf(get().mode)
      const nextMode = MODES[(currentIndex + 1) % MODES.length]
      get().setMode(nextMode)
    },
  }
})

// Keep `effective` (and the DOM attribute) in sync with live OS theme changes while
// `mode === 'system'`. Registered once at module load, mirroring authStore's
// setOnUnauthorized pattern of a top-level side effect tied to the store singleton.
if (typeof window !== 'undefined' && typeof window.matchMedia === 'function') {
  const media = window.matchMedia('(prefers-color-scheme: dark)')
  const handleChange = () => {
    if (useThemeStore.getState().mode !== 'system') {
      return
    }
    const effective = resolveEffective('system')
    applyEffectiveToDom(effective)
    useThemeStore.setState({ effective })
  }
  // addEventListener is the standard API; jsdom's MediaQueryList supports it.
  media.addEventListener('change', handleChange)
}
