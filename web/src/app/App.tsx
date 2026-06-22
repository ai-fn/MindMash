import { useTranslation } from 'react-i18next'

// Placeholder root component for Task 1. Proves Tailwind + i18next +
// render wiring end to end. Routing, providers (QueryClient, Router),
// and real screens land in later tasks (3+).
export function App() {
  const { t } = useTranslation()

  return (
    <main className="flex min-h-screen items-center justify-center bg-slate-50">
      <h1 className="text-3xl font-semibold text-slate-800">{t('app.tagline')}</h1>
    </main>
  )
}
