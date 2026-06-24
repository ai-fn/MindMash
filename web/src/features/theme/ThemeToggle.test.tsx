import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import '../../lib/i18n.ts'

function installMatchMediaMock(prefersDark: boolean) {
  vi.stubGlobal(
    'matchMedia',
    vi.fn(() => ({
      matches: prefersDark,
      media: '(prefers-color-scheme: dark)',
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })),
  )
}

async function loadToggle() {
  const { ThemeToggle } = await import('./ThemeToggle')
  return ThemeToggle
}

describe('ThemeToggle', () => {
  beforeEach(() => {
    vi.resetModules()
    window.localStorage.clear()
    document.documentElement.removeAttribute('data-theme')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('cycles light -> dark -> system on repeated activation and updates the label', async () => {
    installMatchMediaMock(false)
    window.localStorage.setItem('mindmesh.theme', 'light')
    const ThemeToggle = await loadToggle()
    const user = userEvent.setup()

    render(<ThemeToggle />)
    const button = screen.getByRole('button')
    expect(button).toHaveTextContent('Light')

    await user.click(button)
    expect(button).toHaveTextContent('Dark')
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark')

    await user.click(button)
    expect(button).toHaveTextContent('System')

    await user.click(button)
    expect(button).toHaveTextContent('Light')
  })
})
