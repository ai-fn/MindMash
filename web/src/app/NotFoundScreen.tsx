// Catch-all 404. Bare (not wrapped in AppLayout) since an unmatched path may not even be
// under an authenticated context — keeps it simple and themed without depending on nav state.
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'

export function NotFoundScreen() {
  const { t } = useTranslation('nav')

  return (
    <main className="flex min-h-screen flex-col items-center justify-center gap-3 bg-bg p-6 text-center">
      <h1 className="text-xl font-semibold text-fg">{t('notFound.heading')}</h1>
      <p className="max-w-sm text-sm text-muted">{t('notFound.body')}</p>
      <Link
        to="/"
        className="rounded-[var(--radius-md)] px-3 py-1.5 text-sm font-medium text-accent underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-bg"
      >
        {t('notFound.backLink')}
      </Link>
    </main>
  )
}
