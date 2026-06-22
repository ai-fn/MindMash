import { Providers } from './providers'

// Root component: composes the app-wide providers (QueryClient, data router, auth
// bootstrap). See providers.tsx for what each piece does.
export function App() {
  return <Providers />
}
