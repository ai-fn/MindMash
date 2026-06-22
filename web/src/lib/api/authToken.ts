// Minimal token seam consumed by the orval fetch mutator (./mutator.ts).
//
// This module intentionally does NOT depend on the app's Zustand auth store: the
// mutator must work before that store exists/initializes, and must never import app
// state (only `onUnauthorized` is allowed to reach back into the app, via a callback).
//
// Task 3 will introduce the full Zustand auth store, which will wrap and/or replace
// this seam (e.g. by calling `setTokens`/`clearTokens` from store actions, and setting
// `onUnauthorized` to a router-aware redirect). Until then this is the single source of
// truth for token storage used by every generated API call.
//
// Storage choice (MVP): the access token lives only in memory (cleared on reload), the
// refresh token lives in `localStorage`. This is a deliberate XSS tradeoff documented as
// deferred hardening — httpOnly cookies would require backend changes that are out of
// scope here (the backend does not set cookies).

const REFRESH_TOKEN_STORAGE_KEY = 'mindmesh.refreshToken'

let accessToken: string | null = null

export function getAccessToken(): string | null {
  return accessToken
}

export function getRefreshToken(): string | null {
  return localStorage.getItem(REFRESH_TOKEN_STORAGE_KEY)
}

export function setTokens(tokens: { accessToken: string; refreshToken?: string }): void {
  accessToken = tokens.accessToken
  if (tokens.refreshToken) {
    localStorage.setItem(REFRESH_TOKEN_STORAGE_KEY, tokens.refreshToken)
  }
}

export function clearTokens(): void {
  accessToken = null
  localStorage.removeItem(REFRESH_TOKEN_STORAGE_KEY)
}

// Invoked when refresh ultimately fails (no valid session can be recovered). Default
// behavior is a hard redirect to /login; Task 3 may override this with a router-aware
// navigation once the app shell exists.
export let onUnauthorized: () => void = () => {
  window.location.assign('/login')
}

export function setOnUnauthorized(handler: () => void): void {
  onUnauthorized = handler
}
