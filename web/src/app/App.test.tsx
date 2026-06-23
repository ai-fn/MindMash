import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import '../lib/i18n.ts'

// App now just composes Providers (QueryClientProvider + RouterProvider + auth
// bootstrap) — the interesting behavior (routing, guard, auth flow) is covered in
// router.test.tsx and authStore.test.ts. This is a smoke test that the app renders
// without crashing and lands on the login screen for an unauthenticated visitor
// (the default state with no stored refresh token in a fresh jsdom localStorage).
vi.mock('../lib/api/identity/auth-service/auth-service', () => ({
  authServiceLogin: vi.fn(),
  authServiceRefresh: vi.fn(),
  authServiceLogout: vi.fn(),
  authServiceGetMe: vi.fn(),
}))

describe('App', () => {
  it('renders without crashing and shows the login screen when unauthenticated', async () => {
    const { App } = await import('./App.tsx')
    render(<App />)
    expect(await screen.findByRole('heading', { name: 'Log in' })).toBeInTheDocument()
  })
})
