import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, Navigate } from 'react-router-dom'

import { Button, Card, Field, TextInput } from '../../ui'
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
    <main className="flex min-h-screen items-center justify-center bg-bg p-6">
      <Card className="flex w-full max-w-sm flex-col gap-4 p-8">
        <form onSubmit={handleSubmit} className="flex flex-col gap-4">
          <h1 className="text-2xl font-semibold text-fg">{t('register.title')}</h1>

          <Field label={t('register.emailLabel')}>
            {(controlProps) => (
              <TextInput
                {...controlProps}
                type="email"
                required
                autoComplete="email"
                value={email}
                onChange={(event) => setEmail(event.target.value)}
              />
            )}
          </Field>

          <Field label={t('register.passwordLabel')}>
            {(controlProps) => (
              <TextInput
                {...controlProps}
                type="password"
                required
                autoComplete="new-password"
                value={password}
                onChange={(event) => handlePasswordChange(event.target.value)}
              />
            )}
          </Field>

          <Field
            label={t('register.confirmPasswordLabel')}
            error={passwordMismatch ? t('register.passwordMismatch') : undefined}
          >
            {(controlProps) => (
              <TextInput
                {...controlProps}
                type="password"
                required
                autoComplete="new-password"
                value={confirmPassword}
                onChange={(event) => handleConfirmPasswordChange(event.target.value)}
              />
            )}
          </Field>

          {error ? (
            <p role="alert" className="text-sm text-danger">
              {error}
            </p>
          ) : null}

          <Button type="submit" loading={isSubmitting}>
            {isSubmitting ? t('register.submitting') : t('register.submit')}
          </Button>
        </form>

        <Link
          to="/login"
          className="text-center text-sm font-medium text-accent underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-bg"
        >
          {t('register.loginLink')}
        </Link>
      </Card>
    </main>
  )
}
