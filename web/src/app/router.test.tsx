import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import '../lib/i18n.ts'

// Mock the generated identity client at the same module path the auth store imports
// it from, so login/logout in this integration test never hit the network.
vi.mock('../lib/api/identity/auth-service/auth-service', () => ({
  authServiceLogin: vi.fn(),
  authServiceRefresh: vi.fn(),
  authServiceLogout: vi.fn(),
  authServiceGetMe: vi.fn(),
}))

async function renderRouterAt(initialPath: string) {
  // Imported dynamically (after vi.resetModules + mocks are set up) so each test gets
  // a fresh authStore module-level singleton, matching the pattern used in
  // authStore.test.ts and mutator.test.ts.
  const { routes } = await import('./router.tsx')
  const { useAuthStore } = await import('../features/auth/authStore.ts')
  // In the real app, providers.tsx calls bootstrap() once on mount; this test
  // exercises router + ProtectedRoute directly (no <Providers>), so it must trigger
  // the same app-start silent-refresh call itself.
  void useAuthStore.getState().bootstrap()
  const router = createMemoryRouter(routes, { initialEntries: [initialPath] })
  render(<RouterProvider router={router} />)
  return router
}

describe('router + ProtectedRoute integration', () => {
  beforeEach(() => {
    vi.resetModules()
    vi.restoreAllMocks()
    vi.clearAllMocks()
    localStorage.clear()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('redirects an unauthenticated visitor at / to /login without flashing protected content', async () => {
    const authService = await import('../lib/api/identity/auth-service/auth-service')
    // No refresh token in localStorage -> bootstrap() resolves synchronously to
    // 'unauthenticated' with no network call, so the redirect happens immediately.
    await renderRouterAt('/')

    expect(await screen.findByRole('heading', { name: 'Log in' })).toBeInTheDocument()
    expect(screen.queryByText(/Welcome,/)).not.toBeInTheDocument()
    expect(authService.authServiceRefresh).not.toHaveBeenCalled()
  })

  it('drives login -> home -> logout -> redirected back to /login', async () => {
    const authService = await import('../lib/api/identity/auth-service/auth-service')
    vi.mocked(authService.authServiceLogin).mockResolvedValue({
      status: 200,
      data: {
        accessToken: 'access-1',
        refreshToken: 'refresh-1',
        userId: 'user-1',
        profileId: 'profile-1',
        expiresIn: '900',
      },
    } as never)
    vi.mocked(authService.authServiceGetMe).mockResolvedValue({
      status: 200,
      data: {
        userId: 'user-1',
        email: 'person@example.com',
        emailVerified: true,
        locale: 'en',
        profiles: [],
      },
    } as never)
    vi.mocked(authService.authServiceLogout).mockResolvedValue({
      status: 200,
      data: { message: 'ok' },
    } as never)

    const user = userEvent.setup()
    await renderRouterAt('/login')

    expect(screen.getByRole('heading', { name: 'Log in' })).toBeInTheDocument()

    await user.type(screen.getByLabelText('Email'), 'person@example.com')
    await user.type(screen.getByLabelText('Password'), 'hunter2')
    await user.click(screen.getByRole('button', { name: 'Log in' }))

    expect(await screen.findByText('Welcome, person@example.com')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Log out' }))

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Log in' })).toBeInTheDocument()
    })
    expect(authService.authServiceLogout).toHaveBeenCalledWith({ refreshToken: 'refresh-1' })
  })
})
