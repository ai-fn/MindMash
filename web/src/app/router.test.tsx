import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
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

// DecksScreen (the post-login `/` route, see DecksScreen.tsx) calls these
// generated hooks; mock them so this integration test never hits the network.
// An empty deck list keeps post-login assertions on the empty-state message
// rather than real deck content, which is DecksScreen.test.tsx's job to cover.
// DecksScreen also renders NewDeck unconditionally now (Task 7), which pulls in
// useDeckServiceCreateDeck + getDeckServiceListDecksQueryKey.
vi.mock('../lib/api/collection/deck-service/deck-service', () => ({
  useDeckServiceListDecks: vi.fn(() => ({ data: { data: { items: [] } }, isPending: false })),
  useDeckServiceCreateDeck: vi.fn(() => ({ mutate: vi.fn(), isPending: false })),
  getDeckServiceListDecksQueryKey: vi.fn(() => ['/v1/decks']),
}))
vi.mock('../lib/api/scheduler/study-service/study-service', () => ({
  useStudyServiceGetCounts: vi.fn(() => ({ data: undefined, isPending: true })),
  // StudyScreen (the /study/:deckId route, see router.test.tsx's full-bleed-shell
  // test) also pulls these in via useStudyQueue/StudyScreen itself; a pending queue
  // fetch + no preview is enough to render the screen without a real network call.
  useStudyServiceGetQueue: vi.fn(() => ({ data: undefined, isPending: true })),
  useStudyServiceGetPreview: vi.fn(() => ({ data: undefined, isPending: true })),
  useStudyServiceAnswer: vi.fn(() => ({ mutate: vi.fn(), isPending: false })),
  getStudyServiceGetCountsQueryKey: vi.fn(() => ['/v1/study/counts']),
}))
vi.mock('../lib/api/collection/card-service/card-service', () => ({
  useCardServiceGetCard: vi.fn(() => ({ data: undefined, isPending: true })),
}))
// StatsScreen (the /stats route, Task 8) pulls in the analytics-service hooks; a
// pending overview fetch is enough to render the screen's loading skeleton without a
// real network call, mirroring the other generated-hook mocks above.
vi.mock('../lib/api/analytics/analytics-service/analytics-service', () => ({
  useAnalyticsServiceGetOverview: vi.fn(() => ({ data: undefined, isPending: true })),
  useAnalyticsServiceGetHeatmap: vi.fn(() => ({ data: undefined, isPending: true })),
  useAnalyticsServiceGetRetention: vi.fn(() => ({ data: undefined, isPending: true })),
  useAnalyticsServiceGetBreakdown: vi.fn(() => ({ data: undefined, isPending: true })),
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
  // DecksScreen (the post-login `/` route) uses React Query hooks, so this test
  // needs the same QueryClientProvider the real app gets from providers.tsx.
  const queryClient = new QueryClient()
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
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

  it('renders RegisterScreen at /register', async () => {
    await renderRouterAt('/register')

    expect(await screen.findByRole('heading', { name: 'Create account' })).toBeInTheDocument()
  })

  it('redirects an unauthenticated visitor at / to /login without flashing protected content', async () => {
    const authService = await import('../lib/api/identity/auth-service/auth-service')
    // No refresh token in localStorage -> bootstrap() resolves synchronously to
    // 'unauthenticated' with no network call, so the redirect happens immediately.
    await renderRouterAt('/')

    expect(await screen.findByRole('heading', { name: 'Log in' })).toBeInTheDocument()
    expect(screen.queryByText('Your decks')).not.toBeInTheDocument()
    expect(authService.authServiceRefresh).not.toHaveBeenCalled()
  })

  it('drives login -> decks -> logout -> redirected back to /login', async () => {
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

    expect(await screen.findByText('Your decks')).toBeInTheDocument()
    expect(screen.getByText("You don't have any decks yet.")).toBeInTheDocument()
    // The Decks route renders inside AppLayout's shell (brand + nav).
    expect(screen.getByText('MindMesh')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Browse' })).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Log out' }))

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Log in' })).toBeInTheDocument()
    })
    expect(authService.authServiceLogout).toHaveBeenCalledWith({ refreshToken: 'refresh-1' })
  })

  it('renders /browse, /settings inside the AppLayout shell with the active link highlighted', async () => {
    const authService = await import('../lib/api/identity/auth-service/auth-service')
    vi.mocked(authService.authServiceRefresh).mockResolvedValue({
      status: 200,
      data: {
        accessToken: 'access-1',
        refreshToken: 'refresh-1',
        userId: 'user-1',
        profileId: 'profile-1',
        expiresIn: '900',
      },
    } as never)
    localStorage.setItem('mindmesh.refreshToken', 'refresh-0')

    await renderRouterAt('/browse')

    expect(await screen.findByText('Coming soon.')).toBeInTheDocument()
    expect(screen.getByText('MindMesh')).toBeInTheDocument()
    const browseLink = screen.getByRole('link', { name: 'Browse' })
    expect(browseLink).toHaveAttribute('aria-current', 'page')
  })

  it('renders /stats inside the AppLayout shell with the real StatsScreen and the active link highlighted', async () => {
    const authService = await import('../lib/api/identity/auth-service/auth-service')
    vi.mocked(authService.authServiceRefresh).mockResolvedValue({
      status: 200,
      data: {
        accessToken: 'access-1',
        refreshToken: 'refresh-1',
        userId: 'user-1',
        profileId: 'profile-1',
        expiresIn: '900',
      },
    } as never)
    localStorage.setItem('mindmesh.refreshToken', 'refresh-0')

    await renderRouterAt('/stats')

    expect(await screen.findByText('Your stats')).toBeInTheDocument()
    expect(screen.getByText('MindMesh')).toBeInTheDocument()
    const statsLink = screen.getByRole('link', { name: 'Stats' })
    expect(statsLink).toHaveAttribute('aria-current', 'page')
  })

  it('does not render the AppLayout shell/nav on the full-bleed study route', async () => {
    const authService = await import('../lib/api/identity/auth-service/auth-service')
    vi.mocked(authService.authServiceRefresh).mockResolvedValue({
      status: 200,
      data: {
        accessToken: 'access-1',
        refreshToken: 'refresh-1',
        userId: 'user-1',
        profileId: 'profile-1',
        expiresIn: '900',
      },
    } as never)
    localStorage.setItem('mindmesh.refreshToken', 'refresh-0')

    await renderRouterAt('/study/deck-1')

    await waitFor(() => {
      expect(screen.queryByText('MindMesh')).not.toBeInTheDocument()
    })
    expect(screen.queryByRole('link', { name: 'Browse' })).not.toBeInTheDocument()
  })

  it('renders the 404 screen for an unmatched path with a link back to /', async () => {
    await renderRouterAt('/this-route-does-not-exist')

    expect(await screen.findByRole('heading', { name: 'Page not found' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Back to decks' })).toHaveAttribute('href', '/')
  })
})
