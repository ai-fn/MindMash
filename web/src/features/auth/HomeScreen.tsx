import { useTranslation } from 'react-i18next'

import { useAuthStore } from './authStore'

// Minimal authenticated placeholder (Task 3 scope, see task-3-brief.md): greets the
// user and offers Logout, so login -> home -> logout -> redirect-to-login is
// manually testable and has an integration-test anchor. Task 4 replaces these
// *contents* with the real deck picker; the protected `/` route itself doesn't change.
export function HomeScreen() {
  const { t } = useTranslation('auth')
  const email = useAuthStore((state) => state.email)
  const logout = useAuthStore((state) => state.logout)

  return (
    <main className="flex min-h-screen flex-col items-center justify-center gap-6 bg-slate-50">
      <p className="text-xl text-slate-800">{t('home.welcome', { email })}</p>
      <button
        type="button"
        onClick={() => void logout()}
        className="rounded bg-slate-800 px-4 py-2 text-white"
      >
        {t('home.logout')}
      </button>
    </main>
  )
}
