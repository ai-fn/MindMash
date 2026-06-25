import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, Navigate } from 'react-router-dom'

import { Button, Card, Field, TextInput } from '../../ui'
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
    <main className="flex min-h-screen items-center justify-center bg-bg p-6">
      <Card className="flex w-full max-w-sm flex-col gap-4 p-8">
        <form onSubmit={handleSubmit} className="flex flex-col gap-4">
          <h1 className="text-2xl font-semibold text-fg">{t('login.title')}</h1>

          <Field label={t('login.emailLabel')}>
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

          <Field label={t('login.passwordLabel')}>
            {(controlProps) => (
              <TextInput
                {...controlProps}
                type="password"
                required
                autoComplete="current-password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
            )}
          </Field>

          {error ? (
            <p role="alert" className="text-sm text-danger">
              {error}
            </p>
          ) : null}

          <Button type="submit" loading={isSubmitting}>
            {isSubmitting ? t('login.submitting') : t('login.submit')}
          </Button>
        </form>

        <Link
          to="/register"
          className="text-center text-sm font-medium text-accent underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-bg"
        >
          {t('login.createAccountLink')}
        </Link>
      </Card>
    </main>
  )
}
