import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import '../../lib/i18n.ts'

// RegisterScreen calls `useAuthStore` directly (not a generated hook), so the store
// module itself is the mock surface here — mirroring how authStore.test.ts mocks the
// generated client one layer down, and how router.test.tsx mocks at the layer a
// component actually imports from.
vi.mock('./authStore', () => ({
  useAuthStore: vi.fn(),
}))

async function renderScreen() {
  const { RegisterScreen } = await import('./RegisterScreen.tsx')
  return render(
    <MemoryRouter initialEntries={['/register']}>
      <RegisterScreen />
    </MemoryRouter>,
  )
}

describe('RegisterScreen', () => {
  let register: ReturnType<typeof vi.fn>

  beforeEach(async () => {
    vi.clearAllMocks()
    register = vi.fn()
    const { useAuthStore } = await import('./authStore')
    // authStore is a Zustand store hook invoked as `useAuthStore((state) => state.x)`;
    // this mock implementation calls the passed selector against a stub state object,
    // matching real Zustand selector usage closely enough for this component's needs.
    vi.mocked(useAuthStore).mockImplementation((selector: never) =>
      (selector as (state: { status: string; register: typeof register }) => unknown)({
        status: 'unauthenticated',
        register,
      }),
    )
  })

  afterEach(() => {
    vi.resetModules()
  })

  it('renders the email, password, confirm password fields and a submit button', async () => {
    await renderScreen()

    expect(screen.getByLabelText('Email')).toBeInTheDocument()
    expect(screen.getByLabelText('Password')).toBeInTheDocument()
    expect(screen.getByLabelText('Confirm password')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Create account' })).toBeInTheDocument()
  })

  it('shows a mismatch error and does not call register when passwords differ', async () => {
    const user = userEvent.setup()
    await renderScreen()

    await user.type(screen.getByLabelText('Email'), 'person@example.com')
    await user.type(screen.getByLabelText('Password'), 'hunter2')
    await user.type(screen.getByLabelText('Confirm password'), 'different')
    await user.click(screen.getByRole('button', { name: 'Create account' }))

    expect(screen.getByText("Passwords don't match.")).toBeInTheDocument()
    expect(register).not.toHaveBeenCalled()
  })

  it('clears the mismatch error once the two password fields match again', async () => {
    const user = userEvent.setup()
    await renderScreen()

    await user.type(screen.getByLabelText('Email'), 'person@example.com')
    await user.type(screen.getByLabelText('Password'), 'hunter2')
    await user.type(screen.getByLabelText('Confirm password'), 'different')
    await user.click(screen.getByRole('button', { name: 'Create account' }))
    expect(screen.getByText("Passwords don't match.")).toBeInTheDocument()

    await user.clear(screen.getByLabelText('Confirm password'))
    await user.type(screen.getByLabelText('Confirm password'), 'hunter2')

    expect(screen.queryByText("Passwords don't match.")).not.toBeInTheDocument()
  })

  it('on matching passwords, calls register with the email and password (not confirmPassword)', async () => {
    register.mockResolvedValue(undefined)
    const user = userEvent.setup()
    await renderScreen()

    await user.type(screen.getByLabelText('Email'), 'person@example.com')
    await user.type(screen.getByLabelText('Password'), 'hunter2')
    await user.type(screen.getByLabelText('Confirm password'), 'hunter2')
    await user.click(screen.getByRole('button', { name: 'Create account' }))

    expect(register).toHaveBeenCalledWith('person@example.com', 'hunter2')
  })

  it('on failure, shows an inline error and leaves the form usable (not stuck disabled)', async () => {
    register.mockRejectedValue(new Error('email already taken'))
    const user = userEvent.setup()
    await renderScreen()

    await user.type(screen.getByLabelText('Email'), 'person@example.com')
    await user.type(screen.getByLabelText('Password'), 'hunter2')
    await user.type(screen.getByLabelText('Confirm password'), 'hunter2')
    await user.click(screen.getByRole('button', { name: 'Create account' }))

    expect(
      await screen.findByText("We couldn't create your account. Check your details and try again."),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Create account' })).not.toBeDisabled()
  })

  it('renders a link back to /login', async () => {
    await renderScreen()

    expect(screen.getByRole('link', { name: 'Already have an account? Log in' })).toHaveAttribute(
      'href',
      '/login',
    )
  })
})
