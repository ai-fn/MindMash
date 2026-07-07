// Zustand auth store: the app's single source of truth for *session/display* state
// (status, userId, profileId, email, locale, profiles). It deliberately does NOT
// duplicate the raw access/refresh tokens — `lib/api/authToken.ts` remains the single
// source of truth for those (see its module comment for why).
//
// Imperative actions below call the generated identity client's plain async functions
// (NOT the useXxx React Query hooks) per task-3-brief.md: login/logout/bootstrap are not
// React render-phase calls, and using the plain functions keeps this store trivially
// mockable in tests.
import { create } from 'zustand'

import {
  authServiceGetMe,
  authServiceLogin,
  authServiceLogout,
  authServiceRefresh,
  authServiceRegister,
} from '../../lib/api/identity/auth-service/auth-service'
import type { V1ProfileInfo } from '../../lib/api/identity/identityV1IdentityProto.schemas'
import { clearTokens, getRefreshToken, setOnUnauthorized, setTokens } from '../../lib/api/authToken'
import { applyServerLocale } from '../../lib/locale'

// The generated response types are a `{ status: 200; data: T } | { status: <error>; data:
// RpcStatus }` union (see auth-service.ts). In practice `customFetch` already throws on
// any non-2xx response (see mutator.ts), so by the time a call here resolves, `data` is
// always the success shape — this helper documents/enforces that invariant for the type
// checker rather than littering every call site with a status check.
function unwrap<TData>(response: { status: number; data: TData | { message?: string } }): TData {
  if (response.status !== 200) {
    throw new Error('Unexpected non-200 response reached the auth store unwrap helper')
  }
  return response.data as TData
}

export type AuthStatus = 'initializing' | 'authenticated' | 'unauthenticated'

interface AuthSessionFields {
  userId?: string
  profileId?: string
  email?: string
  locale?: string
  profiles?: V1ProfileInfo[]
}

interface AuthState extends AuthSessionFields {
  status: AuthStatus
  bootstrap: () => Promise<void>
  login: (email: string, password: string) => Promise<void>
  register: (email: string, password: string) => Promise<void>
  logout: () => Promise<void>
}

const UNAUTHENTICATED_FIELDS: AuthSessionFields = {
  userId: undefined,
  profileId: undefined,
  email: undefined,
  locale: undefined,
  profiles: undefined,
}

export const useAuthStore = create<AuthState>((set, get) => {
  async function enrichFromGetMe(): Promise<void> {
    const data = unwrap(await authServiceGetMe())
    set({
      status: 'authenticated',
      userId: data.userId,
      email: data.email,
      locale: data.locale,
      profiles: data.profiles,
    })

    // Adopt the account's locale for the UI — but only if the user hasn't explicitly picked a
    // language via the Settings switcher (applyServerLocale enforces that precedence). Lazily
    // loads the ru/zh bundle if needed; fire-and-forget so session hydration isn't blocked on
    // a locale chunk fetch.
    void applyServerLocale(data.locale)
  }

  return {
    status: 'initializing',
    ...UNAUTHENTICATED_FIELDS,

    async bootstrap() {
      const refreshToken = getRefreshToken()
      if (!refreshToken) {
        set({ status: 'unauthenticated', ...UNAUTHENTICATED_FIELDS })
        return
      }

      try {
        const data = unwrap(await authServiceRefresh({ refreshToken }))
        setTokens({ accessToken: data.accessToken ?? '', refreshToken: data.refreshToken })
        await enrichFromGetMe()
      } catch {
        clearTokens()
        set({ status: 'unauthenticated', ...UNAUTHENTICATED_FIELDS })
      }
    },

    async login(email: string, password: string) {
      // Intentionally uncaught: a rejected/non-2xx login should propagate to the
      // caller (LoginScreen) so it can surface the error, rather than being
      // swallowed here the way bootstrap()/logout() swallow their own failures.
      const data = unwrap(await authServiceLogin({ email, password }))
      setTokens({ accessToken: data.accessToken ?? '', refreshToken: data.refreshToken })
      set({ profileId: data.profileId })
      await enrichFromGetMe()
    },

    async register(email: string, password: string) {
      // Intentionally uncaught, same reasoning as login(): a failed registration should
      // propagate to the caller (RegisterScreen) to surface, not be swallowed here.
      unwrap(await authServiceRegister({ email, password, locale: 'en' }))
      // Auto-sign-in: the backend's email verification is a stub and Login doesn't require
      // EmailVerified, so there's no "check your email" gate to send the user through —
      // land them straight in the app, same as a returning user.
      await get().login(email, password)
    },

    async logout() {
      try {
        // getRefreshToken() can be null if logout() is called with no active session
        // (e.g. double-invoked, or after the token already expired); the generated
        // client accepts an undefined refreshToken, so this degrades to a no-op
        // revoke call rather than throwing — local cleanup below still runs.
        await authServiceLogout({ refreshToken: getRefreshToken() ?? undefined })
      } catch {
        // Best-effort revoke: local cleanup must proceed even if the network call fails.
      }
      clearTokens()
      set({ status: 'unauthenticated', ...UNAUTHENTICATED_FIELDS })
    },
  }
})

// Registered once at store creation: a reactive 401 (surfaced by the fetch mutator)
// flows through the same state transition as an explicit logout, but — unlike
// authToken's default handler — does a client-side state reset instead of a hard
// redirect, so ProtectedRoute can react with <Navigate> instead of a full page reload.
setOnUnauthorized(() => {
  useAuthStore.setState({ status: 'unauthenticated', ...UNAUTHENTICATED_FIELDS })
})
