import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, Navigate } from 'react-router-dom'

import { useAuthStore } from './authStore'

// Email/password/confirm-password form. Calls `authStore.register` (not a mutation hook
// directly, mirroring LoginScreen's pattern). `/register` isn't behind ProtectedRoute
// either (same reasoning as `/login`), so this component reacts to `status` becoming
// 'authenticated' (via register()'s auto-sign-in) and navigates to `/`.
export function RegisterScreen() {
  const { t } = useTranslation('auth')
  const status = useAuthStore((state) => state.status)
  const register = useAuthStore((state) => state.register)
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [passwordMismatch, setPasswordMismatch] = useState(false)

  if (status === 'authenticated') {
    return <Navigate to="/" replace />
  }

  function handlePasswordChange(value: string) {
    setPassword(value)
    if (value === confirmPassword) {
      setPasswordMismatch(false)
    }
  }

  function handleConfirmPasswordChange(value: string) {
    setConfirmPassword(value)
    if (value === password) {
      setPasswordMismatch(false)
    }
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setError(null)

    if (password !== confirmPassword) {
      setPasswordMismatch(true)
      return
    }

    setIsSubmitting(true)
    try {
      await register(email, password)
    } catch {
      setError(t('register.errorGeneric'))
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
        <h1 className="text-2xl font-semibold text-slate-800">{t('register.title')}</h1>

        <label className="flex flex-col gap-1 text-sm text-slate-700">
          {t('register.emailLabel')}
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
          {t('register.passwordLabel')}
          <input
            type="password"
            required
            autoComplete="new-password"
            value={password}
            onChange={(event) => handlePasswordChange(event.target.value)}
            className="rounded border border-slate-300 px-3 py-2"
          />
        </label>

        <label className="flex flex-col gap-1 text-sm text-slate-700">
          {t('register.confirmPasswordLabel')}
          <input
            type="password"
            required
            autoComplete="new-password"
            value={confirmPassword}
            onChange={(event) => handleConfirmPasswordChange(event.target.value)}
            className="rounded border border-slate-300 px-3 py-2"
          />
        </label>

        {passwordMismatch && (
          <p className="text-sm text-red-600">{t('register.passwordMismatch')}</p>
        )}
        {error && <p className="text-sm text-red-600">{error}</p>}

        <button
          type="submit"
          disabled={isSubmitting}
          className="rounded bg-slate-800 px-4 py-2 text-white disabled:opacity-50"
        >
          {isSubmitting ? t('register.submitting') : t('register.submit')}
        </button>

        <Link to="/login" className="text-blue-600 underline">
          {t('register.loginLink')}
        </Link>
      </form>
    </main>
  )
}
