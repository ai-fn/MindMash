import { useTranslation } from 'react-i18next'

import { useAuthStore } from '../auth/authStore'
import { ThemeToggle } from '../theme/ThemeToggle'
import { useStudyPrefsStore } from '../study/studyPrefsStore'
import { Button, Card, Switch } from '../../ui'

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
        <Switch
          checked={advancedGrading}
          onChange={toggleAdvancedGrading}
          label={t('study.advancedGrading')}
          description={t('study.advancedGradingDescription')}
        />
        <p className="text-sm text-muted">{t('study.otherPrefsNote')}</p>
      </Card>
    </div>
  )
}
