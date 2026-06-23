import { useEffect, useRef } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from 'react-router-dom'

import { useAuthStore } from '../features/auth/authStore'
import { subscribeSyncChanges } from '../lib/sync'
import { router } from './router'

// `refetchOnWindowFocus: true` already matches TanStack Query v5's own default, but
// Task 8 (see task-8-brief.md) calls for it explicitly configured so it's visible in
// code as an intentional choice rather than an implicit default that could silently
// change with a library upgrade.
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: true,
    },
  },
})

export function Providers() {
  const bootstrap = useAuthStore((state) => state.bootstrap)
  const status = useAuthStore((state) => state.status)
  // StrictMode double-invokes effects in dev; this ref ensures bootstrap() (the
  // app-start silent-refresh call) only ever fires once regardless.
  const hasBootstrapped = useRef(false)

  useEffect(() => {
    if (hasBootstrapped.current) {
      return
    }
    hasBootstrapped.current = true
    void bootstrap()
  }, [bootstrap])

  // Keep multiple tabs/sessions consistent: while authenticated, hold open a single
  // SSE subscription and invalidate the entire query cache on each server-side change
  // notification. No per-entity invalidation map for this lightweight slice — see
  // task-8-brief.md.
  useEffect(() => {
    if (status !== 'authenticated') {
      return
    }
    const unsubscribe = subscribeSyncChanges(() => {
      void queryClient.invalidateQueries()
    })
    return unsubscribe
  }, [status])

  return (
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}
