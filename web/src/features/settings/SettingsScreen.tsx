import { useTranslation } from 'react-i18next'

import { useAuthStore } from '../auth/authStore'
import { ThemeToggle } from '../theme/ThemeToggle'
import { useStudyPrefsStore } from '../study/studyPrefsStore'
import { Button, Card, Switch } from '../../ui'
import { SUPPORTED_LOCALES } from '../../lib/i18n'
import { chooseLocale, isSupportedLocale } from '../../lib/locale'

// Profile & settings (Task 12, see task-12-brief.md). Three sections — Profile, Appearance,
// Study — each a `Card` block, matching DecksScreen.tsx/StatsScreen.tsx's established
// "page heading + sectioned Card" structure. Profile reads display fields straight off
// authStore (populated by enrichFromGetMe() during bootstrap()/login(), see authStore.ts) —
// no fresh GetMe call here. Logout moved here from DecksScreen's header per the brief.
export function SettingsScreen() {
  const { t, i18n } = useTranslation('settings')
  const email = useAuthStore((state) => state.email)
  const userId = useAuthStore((state) => state.userId)
  const logout = useAuthStore((state) => state.logout)
  // The *active UI* language (resolvedLanguage) drives the switcher, not the account `locale`
  // field — an explicit switcher choice can diverge from the stored account locale.
  const activeLocale = i18n.resolvedLanguage ?? 'en'
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
            <dd className="text-fg">
              <select
                aria-label={t('profile.localeSelectLabel')}
                value={activeLocale}
                onChange={(event) => {
                  const next = event.target.value
                  if (isSupportedLocale(next)) void chooseLocale(next)
                }}
                className="rounded-md border border-border bg-surface px-2 py-1 text-fg"
              >
                {SUPPORTED_LOCALES.map((code) => (
                  <option key={code} value={code}>
                    {t(`localeNames.${code}`)}
                  </option>
                ))}
              </select>
            </dd>
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
