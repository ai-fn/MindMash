import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it } from 'vitest'

import '../lib/i18n.ts'
import { useAuthStore } from '../features/auth/authStore'
import { ProtectedRoute } from './guard'

// router.test.tsx already exercises the 'unauthenticated' (redirect) and
// 'authenticated' (Outlet) branches end to end via real login/logout flows.
// This file isolates the 'initializing' branch, which only occurs synchronously
// while bootstrap() is in flight and is otherwise hard to observe in an
// integration test (see Task 3 review).
describe('ProtectedRoute', () => {
  afterEach(() => {
    useAuthStore.setState({ status: 'unauthenticated' })
  })

  it('renders a loading state instead of redirecting while status is initializing', () => {
    useAuthStore.setState({ status: 'initializing' })

    render(
      <MemoryRouter initialEntries={['/']}>
        <Routes>
          <Route element={<ProtectedRoute />}>
            <Route path="/" element={<div>protected content</div>} />
          </Route>
          <Route path="/login" element={<div>login page</div>} />
        </Routes>
      </MemoryRouter>,
    )

    expect(screen.getByText('Loading…')).toBeInTheDocument()
    expect(screen.queryByText('protected content')).not.toBeInTheDocument()
    expect(screen.queryByText('login page')).not.toBeInTheDocument()
  })
})
