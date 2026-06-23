import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, Navigate } from 'react-router-dom'

import { useAuthStore } from './authStore'

// Email/password form. Calls `authStore.login` (not a mutation hook directly, per
// task-3-brief.md). `/login` itself isn't behind ProtectedRoute (it's the escape
// hatch out of it), so this component — not the guard — is what reacts to `status`
// becoming 'authenticated' and navigates to `/`.
export function LoginScreen() {
  const { t } = useTranslation('auth')
  const status = useAuthStore((state) => state.status)
  const login = useAuthStore((state) => state.login)
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  if (status === 'authenticated') {
    return <Navigate to="/" replace />
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setError(null)
    setIsSubmitting(true)
    try {
      await login(email, password)
    } catch {
      setError(t('login.errorGeneric'))
    } finally {
      setIsSubmitting(false)
    }
  }

  return (
    <main className="flex min-h-screen items-center justify-center bg-slate-50">
      <form
        onSubmit={handleSubmit}
        className="flex w-full max-w-sm flex-col gap-4 rounded-lg bg-white p-8 shadow"
      >
        <h1 className="text-2xl font-semibold text-slate-800">{t('login.title')}</h1>

        <label className="flex flex-col gap-1 text-sm text-slate-700">
          {t('login.emailLabel')}
          <input
            type="email"
            required
            autoComplete="email"
            value={email}
            onChange={(event) => setEmail(event.target.value)}
            className="rounded border border-slate-300 px-3 py-2"
          />
        </label>

        <label className="flex flex-col gap-1 text-sm text-slate-700">
          {t('login.passwordLabel')}
          <input
            type="password"
            required
            autoComplete="current-password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            className="rounded border border-slate-300 px-3 py-2"
          />
        </label>

        {error && <p className="text-sm text-red-600">{error}</p>}

        <button
          type="submit"
          disabled={isSubmitting}
          className="rounded bg-slate-800 px-4 py-2 text-white disabled:opacity-50"
        >
          {isSubmitting ? t('login.submitting') : t('login.submit')}
        </button>

        <Link to="/register" className="text-blue-600 underline">
          {t('login.createAccountLink')}
        </Link>
      </form>
    </main>
  )
}
