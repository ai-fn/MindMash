import { useTranslation } from 'react-i18next'

import { useAuthStore } from '../auth/authStore'
import { ThemeToggle } from '../theme/ThemeToggle'
import { useStudyPrefsStore } from '../study/studyPrefsStore'
import { Button, Card } from '../../ui'

// Profile & settings (Task 12, see task-12-brief.md). Three sections — Profile, Appearance,
// Study — each a `Card` block, matching DecksScreen.tsx/StatsScreen.tsx's established
// "page heading + sectioned Card" structure. Profile reads display fields straight off
// authStore (populated by enrichFromGetMe() during bootstrap()/login(), see authStore.ts) —
// no fresh GetMe call here. Logout moved here from DecksScreen's header per the brief.
export function SettingsScreen() {
  const { t } = useTranslation('settings')
  const email = useAuthStore((state) => state.email)
  const userId = useAuthStore((state) => state.userId)
  const locale = useAuthStore((state) => state.locale)
  const logout = useAuthStore((state) => state.logout)
  const advancedGrading = useStudyPrefsStore((state) => state.advancedGrading)
  const toggleAdvancedGrading = useStudyPrefsStore((state) => state.toggle)

  return (
    <div className="flex flex-col gap-6 p-6">
      <h1 className="text-xl font-semibold text-fg">{t('title')}</h1>

      <Card className="flex flex-col gap-4">
        <h2 className="text-base font-semibold text-fg">{t('profile.title')}</h2>
        <dl className="flex flex-col gap-2 text-sm">
          <div className="flex items-center justify-between gap-3">
            <dt className="text-muted">{t('profile.email')}</dt>
            <dd className="text-fg">{email ?? '—'}</dd>
          </div>
          <div className="flex items-center justify-between gap-3">
            <dt className="text-muted">{t('profile.userId')}</dt>
            <dd className="text-fg">{userId ?? '—'}</dd>
          </div>
          <div className="flex items-center justify-between gap-3">
            <dt className="text-muted">{t('profile.locale')}</dt>
            {/* MVP is English-only (see lib/i18n.ts's module comment); rendered as plain text
                rather than a single-option <select> since a disabled dropdown with one option
                would imply more affordance than actually exists. Locale switching UI lands
                with Phase 2 multi-locale support. */}
            <dd className="text-fg">{locale ?? 'en'}</dd>
          </div>
        </dl>
        <div>
          <Button variant="secondary" size="sm" onClick={() => void logout()}>
            {t('profile.logout')}
          </Button>
        </div>
      </Card>

      <Card className="flex flex-col gap-4">
        <h2 className="text-base font-semibold text-fg">{t('appearance.title')}</h2>
        <ThemeToggle />
      </Card>

      <Card className="flex flex-col gap-4">
        <h2 className="text-base font-semibold text-fg">{t('study.title')}</h2>
        <button
          type="button"
          role="switch"
          aria-checked={advancedGrading}
          onClick={toggleAdvancedGrading}
          className="flex items-center justify-between gap-4 rounded-[var(--radius-md)] border border-border bg-surface px-3 py-2 text-left transition-colors duration-[var(--duration-fast)] ease-default hover:bg-accent-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-bg"
        >
          <span className="flex flex-col gap-0.5">
            <span className="text-sm font-medium text-fg">{t('study.advancedGrading')}</span>
            <span className="text-sm text-muted">{t('study.advancedGradingDescription')}</span>
          </span>
          <span
            aria-hidden="true"
            className={`relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors duration-[var(--duration-fast)] ease-default ${
              advancedGrading ? 'bg-accent' : 'bg-border'
            }`}
          >
            <span
              className={`inline-block h-5 w-5 transform rounded-full bg-surface transition-transform duration-[var(--duration-fast)] ease-default ${
                advancedGrading ? 'translate-x-5' : 'translate-x-0.5'
              }`}
            />
          </span>
        </button>
        <p className="text-sm text-muted">{t('study.otherPrefsNote')}</p>
      </Card>
    </div>
  )
}
