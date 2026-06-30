import { useTranslation } from 'react-i18next'
import { Navigate, Outlet } from 'react-router-dom'

import { useAuthStore } from '../features/auth/authStore'

// Gates nested routes on auth status. While `status === 'initializing'` (bootstrap()'s
// silent-refresh call may still be in flight) we render a loading state rather than
// redirecting, to avoid a flash redirect to /login for an already-authenticated user
// reloading the page.
export function ProtectedRoute() {
  const { t } = useTranslation('auth')
  const status = useAuthStore((state) => state.status)

  if (status === 'initializing') {
    return (
      <main className="flex min-h-screen items-center justify-center bg-bg">
        <p className="text-muted">{t('guard.loading')}</p>
      </main>
    )
  }

  if (status === 'unauthenticated') {
    return <Navigate to="/login" replace />
  }

  return <Outlet />
}
