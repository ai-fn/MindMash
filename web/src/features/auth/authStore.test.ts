import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// Mock the generated identity client's plain async functions (per task-3-brief.md: the
// store calls authServiceLogin/authServiceRefresh/authServiceLogout/authServiceGetMe
// directly, never the useXxx hooks, which makes mocking trivial here).
vi.mock('../../lib/api/identity/auth-service/auth-service', () => ({
  authServiceLogin: vi.fn(),
  authServiceRefresh: vi.fn(),
  authServiceLogout: vi.fn(),
  authServiceGetMe: vi.fn(),
}))

vi.mock('../../lib/api/authToken', () => ({
  getAccessToken: vi.fn(),
  getRefreshToken: vi.fn(),
  setTokens: vi.fn(),
  clearTokens: vi.fn(),
  setOnUnauthorized: vi.fn(),
}))

// Modules are dynamically imported fresh per test (vi.resetModules) so the store's
// module-level singleton (created at import time, including its setOnUnauthorized
// registration) never leaks state or registered callbacks across tests.
async function loadModules() {
  const authToken = await import('../../lib/api/authToken')
  const authService = await import('../../lib/api/identity/auth-service/auth-service')
  const { useAuthStore } = await import('./authStore')
  return { authToken, authService, useAuthStore }
}

describe('authStore', () => {
  beforeEach(() => {
    vi.resetModules()
    vi.restoreAllMocks()
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  describe('bootstrap', () => {
    it('sets unauthenticated and makes no network calls when there is no refresh token', async () => {
      const { authToken, authService, useAuthStore } = await loadModules()
      vi.mocked(authToken.getRefreshToken).mockReturnValue(null)

      await useAuthStore.getState().bootstrap()

      expect(useAuthStore.getState().status).toBe('unauthenticated')
      expect(authService.authServiceRefresh).not.toHaveBeenCalled()
      expect(authService.authServiceGetMe).not.toHaveBeenCalled()
    })

    it('on successful refresh, calls GetMe and becomes authenticated with populated fields', async () => {
      const { authToken, authService, useAuthStore } = await loadModules()
      vi.mocked(authToken.getRefreshToken).mockReturnValue('refresh-abc')
      vi.mocked(authService.authServiceRefresh).mockResolvedValue({
        status: 200,
        data: { accessToken: 'access-new', refreshToken: 'refresh-new', expiresIn: '900' },
      } as never)
      vi.mocked(authService.authServiceGetMe).mockResolvedValue({
        status: 200,
        data: {
          userId: 'user-1',
          email: 'person@example.com',
          emailVerified: true,
          locale: 'en',
          profiles: [{ id: 'profile-1', name: 'Default', isDefault: true }],
        },
      } as never)

      await useAuthStore.getState().bootstrap()

      expect(authToken.setTokens).toHaveBeenCalledWith({
        accessToken: 'access-new',
        refreshToken: 'refresh-new',
      })
      expect(authService.authServiceGetMe).toHaveBeenCalledTimes(1)
      const state = useAuthStore.getState()
      expect(state.status).toBe('authenticated')
      expect(state.userId).toBe('user-1')
      expect(state.email).toBe('person@example.com')
      expect(state.locale).toBe('en')
      expect(state.profiles).toEqual([{ id: 'profile-1', name: 'Default', isDefault: true }])
    })

    it('on refresh failure, clears tokens and becomes unauthenticated', async () => {
      const { authToken, authService, useAuthStore } = await loadModules()
      vi.mocked(authToken.getRefreshToken).mockReturnValue('refresh-abc')
      vi.mocked(authService.authServiceRefresh).mockRejectedValue(new Error('refresh failed'))

      await useAuthStore.getState().bootstrap()

      expect(authToken.clearTokens).toHaveBeenCalledTimes(1)
      expect(authService.authServiceGetMe).not.toHaveBeenCalled()
      expect(useAuthStore.getState().status).toBe('unauthenticated')
    })

    it('if refresh succeeds but GetMe fails, clears tokens and becomes unauthenticated', async () => {
      const { authToken, authService, useAuthStore } = await loadModules()
      vi.mocked(authToken.getRefreshToken).mockReturnValue('refresh-abc')
      vi.mocked(authService.authServiceRefresh).mockResolvedValue({
        status: 200,
        data: { accessToken: 'access-new', refreshToken: 'refresh-new', expiresIn: '900' },
      } as never)
      vi.mocked(authService.authServiceGetMe).mockRejectedValue(new Error('getMe failed'))

      await useAuthStore.getState().bootstrap()

      expect(authToken.clearTokens).toHaveBeenCalledTimes(1)
      expect(useAuthStore.getState().status).toBe('unauthenticated')
    })
  })

  describe('login', () => {
    it('on success, stores tokens and populates state via GetMe', async () => {
      const { authToken, authService, useAuthStore } = await loadModules()
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
          profiles: [{ id: 'profile-1', name: 'Default', isDefault: true }],
        },
      } as never)

      await useAuthStore.getState().login('person@example.com', 'hunter2')

      expect(authToken.setTokens).toHaveBeenCalledWith({
        accessToken: 'access-1',
        refreshToken: 'refresh-1',
      })
      const state = useAuthStore.getState()
      expect(state.status).toBe('authenticated')
      expect(state.userId).toBe('user-1')
      expect(state.profileId).toBe('profile-1')
      expect(state.email).toBe('person@example.com')
    })

    it('on failure, stays unauthenticated and rethrows the error', async () => {
      const { authService, useAuthStore } = await loadModules()
      useAuthStore.setState({ status: 'unauthenticated' })
      const loginError = new Error('invalid credentials')
      vi.mocked(authService.authServiceLogin).mockRejectedValue(loginError)

      await expect(useAuthStore.getState().login('person@example.com', 'wrong')).rejects.toThrow(
        'invalid credentials',
      )

      expect(useAuthStore.getState().status).toBe('unauthenticated')
    })
  })

  describe('logout', () => {
    it('calls backend logout, clears tokens, and resets state to unauthenticated', async () => {
      const { authToken, authService, useAuthStore } = await loadModules()
      vi.mocked(authToken.getRefreshToken).mockReturnValue('refresh-abc')
      vi.mocked(authService.authServiceLogout).mockResolvedValue({
        status: 200,
        data: { message: 'ok' },
      } as never)

      await useAuthStore.getState().logout()

      expect(authService.authServiceLogout).toHaveBeenCalledWith({ refreshToken: 'refresh-abc' })
      expect(authToken.clearTokens).toHaveBeenCalledTimes(1)
      const state = useAuthStore.getState()
      expect(state.status).toBe('unauthenticated')
      expect(state.userId).toBeUndefined()
      expect(state.email).toBeUndefined()
    })

    it('still clears tokens and resets state even when the backend call rejects', async () => {
      const { authToken, authService, useAuthStore } = await loadModules()
      vi.mocked(authToken.getRefreshToken).mockReturnValue('refresh-abc')
      vi.mocked(authService.authServiceLogout).mockRejectedValue(new Error('network down'))

      await useAuthStore.getState().logout()

      expect(authToken.clearTokens).toHaveBeenCalledTimes(1)
      expect(useAuthStore.getState().status).toBe('unauthenticated')
    })
  })

  describe('onUnauthorized override', () => {
    it('registers a handler at store creation that resets state without a hard redirect', async () => {
      const { authToken, useAuthStore } = await loadModules()

      expect(authToken.setOnUnauthorized).toHaveBeenCalledTimes(1)
      const registeredHandler = vi.mocked(authToken.setOnUnauthorized).mock.calls[0][0]

      // Simulate an authenticated session, then trigger the registered callback as the
      // mutator would on a reactive 401.
      useAuthStore.setState({
        status: 'authenticated',
        userId: 'user-1',
        profileId: 'profile-1',
        email: 'person@example.com',
        locale: 'en',
        profiles: [],
      })

      // authToken itself is mocked in this test, so there is no real navigation
      // path to assert against directly. What matters is that the registered
      // callback resets store state synchronously and does not throw/attempt a
      // page navigation side effect — the real authToken.ts default handler (a
      // hard `window.location.assign('/login')`) is exactly what this override
      // replaces, per the brief.
      registeredHandler()

      expect(useAuthStore.getState().status).toBe('unauthenticated')
      expect(useAuthStore.getState().userId).toBeUndefined()
    })
  })
})
