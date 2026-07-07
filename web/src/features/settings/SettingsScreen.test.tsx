import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import '../../lib/i18n.ts'
import { useAuthStore } from '../auth/authStore'

function installMatchMediaMock() {
  vi.stubGlobal(
    'matchMedia',
    vi.fn(() => ({
      matches: false,
      media: '(prefers-color-scheme: dark)',
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })),
  )
}

// Unlike DecksScreen.test.tsx, this suite resets modules between tests so that
// studyPrefsStore's/themeStore's module-level `readPersisted()`/matchMedia-resolution
// (evaluated once at import time, see studyPrefsStore.ts and themeStore.test.ts) picks
// up each test's localStorage state — same rationale as themeStore.test.ts. authStore
// is re-imported fresh each time alongside SettingsScreen for the same reason (it must
// be the *same* module instance SettingsScreen's `useAuthStore` import resolves to).
async function renderScreen() {
  const { SettingsScreen } = await import('./SettingsScreen.tsx')
  const { useAuthStore: freshUseAuthStore } = await import('../auth/authStore')
  freshUseAuthStore.setState({
    status: 'authenticated',
    email: 'rin@example.com',
    userId: 'user-123',
    locale: 'en',
  })
  return { ...render(<SettingsScreen />), useAuthStore: freshUseAuthStore }
}

describe('SettingsScreen', () => {
  beforeEach(() => {
    vi.resetModules()
    installMatchMediaMock()
    window.localStorage.clear()
    document.documentElement.removeAttribute('data-theme')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    useAuthStore.setState({ status: 'unauthenticated' })
  })

  it('renders profile info from authStore without issuing a fresh GetMe call', async () => {
    await renderScreen()

    expect(screen.getByText('rin@example.com')).toBeInTheDocument()
    expect(screen.getByText('user-123')).toBeInTheDocument()
  })

  it('renders a language switcher defaulting to the active locale', async () => {
    await renderScreen()

    const select = screen.getByRole('combobox', { name: 'Interface language' })
    expect(select).toHaveValue('en')
    expect(screen.getByRole('option', { name: 'English' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'Русский' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: '中文' })).toBeInTheDocument()
  })

  it('switches the UI language when a new locale is chosen', async () => {
    const user = userEvent.setup()
    await renderScreen()

    await user.selectOptions(screen.getByRole('combobox', { name: 'Interface language' }), 'ru')

    // The heading (settings:title) re-renders in Russian once the ru bundle lazy-loads.
    expect(await screen.findByRole('heading', { name: 'Настройки' })).toBeInTheDocument()
  })

  it('calls logout when the Log out button is clicked', async () => {
    const { useAuthStore: freshUseAuthStore } = await renderScreen()
    const logoutSpy = vi.fn().mockResolvedValue(undefined)
    freshUseAuthStore.setState({ logout: logoutSpy })
    const user = userEvent.setup()

    await user.click(screen.getByRole('button', { name: 'Log out' }))

    expect(logoutSpy).toHaveBeenCalledTimes(1)
  })

  it('renders the theme toggle in the Appearance section', async () => {
    await renderScreen()

    expect(screen.getByRole('button', { name: /current theme/i })).toBeInTheDocument()
  })

  it('toggles advanced grading and persists it to localStorage', async () => {
    const user = userEvent.setup()
    await renderScreen()

    const toggle = screen.getByRole('switch', { name: /advanced grading/i })
    expect(toggle).toHaveAttribute('aria-checked', 'false')
    expect(window.localStorage.getItem('mindmesh.study.advancedGrading')).toBeNull()

    await user.click(toggle)

    expect(toggle).toHaveAttribute('aria-checked', 'true')
    expect(window.localStorage.getItem('mindmesh.study.advancedGrading')).toBe('true')

    await user.click(toggle)

    expect(toggle).toHaveAttribute('aria-checked', 'false')
    expect(window.localStorage.getItem('mindmesh.study.advancedGrading')).toBe('false')
  })

  it('reflects a previously-persisted advanced grading preference on load', async () => {
    window.localStorage.setItem('mindmesh.study.advancedGrading', 'true')

    await renderScreen()

    expect(screen.getByRole('switch', { name: /advanced grading/i })).toHaveAttribute(
      'aria-checked',
      'true',
    )
  })
})
