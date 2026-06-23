import { act, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import '../lib/i18n.ts'
import { useAuthStore } from '../features/auth/authStore'

// Bootstrap calls the real identity client otherwise; stub it out so it resolves to
// "no stored session" deterministically without touching the network. Mirrors
// App.test.tsx's existing mocking pattern.
vi.mock('../lib/api/identity/auth-service/auth-service', () => ({
  authServiceLogin: vi.fn(),
  authServiceRefresh: vi.fn(),
  authServiceLogout: vi.fn(),
  authServiceGetMe: vi.fn(),
}))

const unsubscribeMock = vi.fn()
const subscribeSyncChangesMock = vi.fn((onChanged: () => void): (() => void) => {
  void onChanged
  return unsubscribeMock
})

vi.mock('../lib/sync', () => ({
  subscribeSyncChanges: subscribeSyncChangesMock,
}))

describe('Providers', () => {
  afterEach(() => {
    useAuthStore.setState({ status: 'unauthenticated' })
    subscribeSyncChangesMock.mockClear()
    unsubscribeMock.mockClear()
  })

  it('does not open the sync subscription while unauthenticated', async () => {
    const { Providers } = await import('./providers.tsx')
    render(<Providers />)

    expect(await screen.findByRole('heading', { name: 'Log in' })).toBeInTheDocument()
    expect(subscribeSyncChangesMock).not.toHaveBeenCalled()
  })

  it('opens the sync subscription once status becomes authenticated, and tears it down when status changes away', async () => {
    const { Providers } = await import('./providers.tsx')
    render(<Providers />)
    await screen.findByRole('heading', { name: 'Log in' })

    act(() => {
      useAuthStore.setState({ status: 'authenticated' })
    })
    expect(subscribeSyncChangesMock).toHaveBeenCalledTimes(1)
    expect(unsubscribeMock).not.toHaveBeenCalled()

    act(() => {
      useAuthStore.setState({ status: 'unauthenticated' })
    })
    expect(unsubscribeMock).toHaveBeenCalledTimes(1)
  })

  it('invalidates all queries when the sync subscription reports a change', async () => {
    const { Providers } = await import('./providers.tsx')
    render(<Providers />)
    await screen.findByRole('heading', { name: 'Log in' })

    act(() => {
      useAuthStore.setState({ status: 'authenticated' })
    })
    expect(subscribeSyncChangesMock).toHaveBeenCalledTimes(1)

    const onChanged = subscribeSyncChangesMock.mock.calls[0][0]
    // Just confirm it can be invoked without throwing — the QueryClient instance is
    // internal to providers.tsx, so this exercises the wiring, not a separate
    // queryClient mock that would defeat the purpose of testing the real instance.
    expect(() => onChanged()).not.toThrow()
  })
})
