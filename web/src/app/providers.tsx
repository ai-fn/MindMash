import { useEffect, useRef } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from 'react-router-dom'

import { useAuthStore } from '../features/auth/authStore'
import { router } from './router'

// QueryClient instance configuration (retry/staleTime tuning) is Task 8 scope — a
// bare default instance is enough to compose the providers here.
const queryClient = new QueryClient()

export function Providers() {
  const bootstrap = useAuthStore((state) => state.bootstrap)
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

  return (
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}
