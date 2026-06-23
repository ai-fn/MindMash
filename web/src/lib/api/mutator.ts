// Custom fetch mutator consumed by every orval-generated client (see ../../../orval.config.ts).
//
// Responsibilities (see task-2-brief.md):
//   1. Resolve a base URL from VITE_API_BASE_URL, defaulting to '' (relative URL, routed
//      through the Vite dev proxy in vite.config.ts). Never hardcode service hosts.
//   2. Inject `Authorization: Bearer <accessToken>` from the authToken seam when present.
//   3. On a 401, perform a single-flight refresh and retry the original request once;
//      the refresh endpoint itself is never retried (no loop).
//   4. Throw on non-2xx responses so TanStack Query treats them as errors.
//
// This module must never import the router or app state — redirect-on-logout is
// delegated to authToken.onUnauthorized, which Task 3's auth store may override.
import {
  clearTokens,
  getAccessToken,
  getRefreshToken,
  onUnauthorized,
  setTokens,
} from './authToken'

const REFRESH_PATH = '/v1/auth/refresh'

export class ApiError extends Error {
  status: number
  info: unknown

  constructor(status: number, info: unknown) {
    super(`API request failed with status ${status}`)
    this.name = 'ApiError'
    this.status = status
    this.info = info
  }
}

function resolveUrl(path: string): string {
  const baseUrl = import.meta.env.VITE_API_BASE_URL ?? ''
  return `${baseUrl}${path}`
}

async function parseBody<T>(response: Response): Promise<T> {
  if ([204, 205, 304].includes(response.status)) {
    return undefined as T
  }
  const text = await response.text()
  if (!text) {
    return undefined as T
  }
  const contentType = response.headers.get('content-type') ?? ''
  if (contentType.includes('json')) {
    return JSON.parse(text) as T
  }
  return text as T
}

// Every orval-generated response type (e.g. authServiceLoginResponse) is a
// `{status, data, headers}` union, not a bare body — callers across the app (authStore's
// `unwrap`, every screen's `data?.data` read) rely on that envelope shape.
async function toEnvelope<T>(response: Response): Promise<T> {
  const data = await parseBody<unknown>(response)
  return { status: response.status, data, headers: response.headers } as T
}

function withAuthHeader(options: RequestInit): RequestInit {
  const accessToken = getAccessToken()
  if (!accessToken) {
    return options
  }
  const headers = new Headers(options.headers)
  headers.set('Authorization', `Bearer ${accessToken}`)
  return { ...options, headers }
}

// Module-level in-flight refresh promise shared by all concurrent 401s, so a burst of
// simultaneously-expiring requests triggers exactly one refresh call (no stampede).
let inFlightRefresh: Promise<boolean> | null = null

async function refreshTokens(): Promise<boolean> {
  const refreshToken = getRefreshToken()
  if (!refreshToken) {
    return false
  }

  try {
    // Direct fetch, NOT customFetch — going through customFetch would recurse into this
    // same 401-handling logic.
    const response = await fetch(resolveUrl(REFRESH_PATH), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refreshToken }),
    })
    if (!response.ok) {
      return false
    }
    const data = await parseBody<{ accessToken: string; refreshToken?: string }>(response)
    setTokens({ accessToken: data.accessToken, refreshToken: data.refreshToken })
    return true
  } catch {
    return false
  }
}

function refreshOnce(): Promise<boolean> {
  if (!inFlightRefresh) {
    inFlightRefresh = refreshTokens().finally(() => {
      inFlightRefresh = null
    })
  }
  return inFlightRefresh
}

async function failUnauthorized(response: Response): Promise<never> {
  clearTokens()
  onUnauthorized()
  const info = await parseBody<unknown>(response)
  throw new ApiError(response.status, info)
}

export async function customFetch<T>(url: string, options: RequestInit): Promise<T> {
  const isRefreshCall = url === REFRESH_PATH
  const requestInit = withAuthHeader(options)
  const response = await fetch(resolveUrl(url), requestInit)

  if (response.status !== 401) {
    if (!response.ok) {
      const info = await parseBody<unknown>(response)
      throw new ApiError(response.status, info)
    }
    return toEnvelope<T>(response)
  }

  // A 401 from the refresh endpoint itself means the refresh token is no longer valid —
  // never attempt to refresh the refresh call.
  if (isRefreshCall) {
    return failUnauthorized(response)
  }

  const refreshed = await refreshOnce()
  if (!refreshed) {
    return failUnauthorized(response)
  }

  const retryInit = withAuthHeader(options)
  const retryResponse = await fetch(resolveUrl(url), retryInit)
  if (!retryResponse.ok) {
    if (retryResponse.status === 401) {
      clearTokens()
      onUnauthorized()
    }
    const info = await parseBody<unknown>(retryResponse)
    throw new ApiError(retryResponse.status, info)
  }
  return toEnvelope<T>(retryResponse)
}

export default customFetch
