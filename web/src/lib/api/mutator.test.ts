import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// customFetch (and the authToken seam it depends on) are imported fresh per test via
// dynamic import + vi.resetModules(), so the mutator's module-level in-flight-refresh
// promise — and any spies installed on authToken — never leak across tests. Both
// modules must come from the same dynamic-import pass so spies observe the instance
// mutator.ts actually calls.
async function loadModules() {
  const authToken = await import('./authToken')
  const mutator = await import('./mutator')
  return { authToken, customFetch: mutator.customFetch }
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

describe('customFetch', () => {
  beforeEach(() => {
    vi.resetModules()
    vi.restoreAllMocks()
    localStorage.clear()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('injects the bearer token from authToken.getAccessToken when present', async () => {
    const { authToken, customFetch } = await loadModules()
    authToken.setTokens({ accessToken: 'access-123' })
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ ok: true }))
    vi.stubGlobal('fetch', fetchMock)

    await customFetch('/v1/decks', { method: 'GET' })

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    const headers = new Headers(init.headers)
    expect(headers.get('Authorization')).toBe('Bearer access-123')
  })

  it('does not inject an Authorization header when there is no access token', async () => {
    const { customFetch } = await loadModules()
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ ok: true }))
    vi.stubGlobal('fetch', fetchMock)

    await customFetch('/v1/decks', { method: 'GET' })

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    const headers = new Headers(init.headers)
    expect(headers.has('Authorization')).toBe(false)
  })

  it('on 401: refreshes once, retries the original request, and stores new tokens', async () => {
    const { authToken, customFetch } = await loadModules()
    authToken.setTokens({ accessToken: 'expired-access', refreshToken: 'refresh-abc' })
    const setTokensSpy = vi.spyOn(authToken, 'setTokens')

    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (url === '/v1/auth/refresh') {
        return Promise.resolve(
          jsonResponse({ accessToken: 'new-access', refreshToken: 'new-refresh', expiresIn: 900 }),
        )
      }
      if (url === '/v1/decks') {
        const headers = new Headers(init?.headers)
        if (headers.get('Authorization') === 'Bearer new-access') {
          return Promise.resolve(jsonResponse({ decks: [] }))
        }
        return Promise.resolve(jsonResponse({ message: 'unauthorized' }, 401))
      }
      throw new Error(`unexpected url ${url}`)
    })
    vi.stubGlobal('fetch', fetchMock)

    const result = await customFetch('/v1/decks', { method: 'GET' })

    expect(result).toEqual({ decks: [] })
    expect(setTokensSpy).toHaveBeenCalledWith({
      accessToken: 'new-access',
      refreshToken: 'new-refresh',
    })

    const deckCalls = fetchMock.mock.calls.filter(([url]) => url === '/v1/decks')
    expect(deckCalls).toHaveLength(2)
    const refreshCalls = fetchMock.mock.calls.filter(([url]) => url === '/v1/auth/refresh')
    expect(refreshCalls).toHaveLength(1)
  })

  it('on 401 with a failing refresh: clears tokens, calls onUnauthorized, and rejects', async () => {
    const { authToken, customFetch } = await loadModules()
    authToken.setTokens({ accessToken: 'expired-access', refreshToken: 'refresh-abc' })
    const clearTokensSpy = vi.spyOn(authToken, 'clearTokens')
    const onUnauthorizedSpy = vi.fn()
    authToken.setOnUnauthorized(onUnauthorizedSpy)

    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url === '/v1/auth/refresh') {
        return Promise.resolve(jsonResponse({ message: 'invalid refresh token' }, 401))
      }
      if (url === '/v1/decks') {
        return Promise.resolve(jsonResponse({ message: 'unauthorized' }, 401))
      }
      throw new Error(`unexpected url ${url}`)
    })
    vi.stubGlobal('fetch', fetchMock)

    await expect(customFetch('/v1/decks', { method: 'GET' })).rejects.toBeTruthy()

    expect(clearTokensSpy).toHaveBeenCalledTimes(1)
    expect(onUnauthorizedSpy).toHaveBeenCalledTimes(1)
  })

  it('does not attempt to refresh a 401 from the refresh endpoint itself (no loop)', async () => {
    const { authToken, customFetch } = await loadModules()
    authToken.setTokens({ accessToken: 'expired-access', refreshToken: 'refresh-abc' })
    const clearTokensSpy = vi.spyOn(authToken, 'clearTokens')
    const onUnauthorizedSpy = vi.fn()
    authToken.setOnUnauthorized(onUnauthorizedSpy)

    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url === '/v1/auth/refresh') {
        return Promise.resolve(jsonResponse({ message: 'invalid refresh token' }, 401))
      }
      throw new Error(`unexpected url ${url}`)
    })
    vi.stubGlobal('fetch', fetchMock)

    await expect(
      customFetch('/v1/auth/refresh', {
        method: 'POST',
        body: JSON.stringify({ refreshToken: 'refresh-abc' }),
      }),
    ).rejects.toBeTruthy()

    const refreshCalls = fetchMock.mock.calls.filter(([url]) => url === '/v1/auth/refresh')
    expect(refreshCalls).toHaveLength(1)
    expect(clearTokensSpy).toHaveBeenCalledTimes(1)
    expect(onUnauthorizedSpy).toHaveBeenCalledTimes(1)
  })

  it('shares a single in-flight refresh across two concurrent 401s (no stampede)', async () => {
    const { authToken, customFetch } = await loadModules()
    authToken.setTokens({ accessToken: 'expired-access', refreshToken: 'refresh-abc' })

    let refreshCallCount = 0
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (url === '/v1/auth/refresh') {
        refreshCallCount += 1
        return Promise.resolve(
          jsonResponse({ accessToken: 'new-access', refreshToken: 'new-refresh', expiresIn: 900 }),
        )
      }
      const headers = new Headers(init?.headers)
      if (headers.get('Authorization') === 'Bearer new-access') {
        return Promise.resolve(jsonResponse({ ok: true }))
      }
      return Promise.resolve(jsonResponse({ message: 'unauthorized' }, 401))
    })
    vi.stubGlobal('fetch', fetchMock)

    const [resultA, resultB] = await Promise.all([
      customFetch('/v1/decks', { method: 'GET' }),
      customFetch('/v1/cards/123', { method: 'GET' }),
    ])

    expect(resultA).toEqual({ ok: true })
    expect(resultB).toEqual({ ok: true })
    expect(refreshCallCount).toBe(1)
  })

  it('resolves base URL from VITE_API_BASE_URL when set, otherwise uses a relative URL', async () => {
    const { customFetch } = await loadModules()
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ ok: true }))
    vi.stubGlobal('fetch', fetchMock)

    await customFetch('/v1/decks', { method: 'GET' })

    const [url] = fetchMock.mock.calls[0] as [string, RequestInit]
    // No VITE_API_BASE_URL configured in the test env -> base resolves to '' -> relative URL.
    expect(url).toBe('/v1/decks')
  })

  it('surfaces non-2xx responses as rejected promises (non-401 error)', async () => {
    const { customFetch } = await loadModules()
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ message: 'boom' }, 500))
    vi.stubGlobal('fetch', fetchMock)

    await expect(customFetch('/v1/decks', { method: 'GET' })).rejects.toBeTruthy()
  })
})
