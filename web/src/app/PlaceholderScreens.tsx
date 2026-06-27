// Minimal stand-ins for routes whose real screens land in later tasks (Browse: search,
// Settings: spec 12 profile/theme/locale). Each renders inside AppLayout's <Outlet/> so nav
// links never 404 — see task-3-brief.md judgment call 2. Intentionally tiny: no
// feature-folder scaffolding (data fetching, sub-components) until the task that actually
// owns that screen. Stats (spec 06 analytics) got its real screen in Task 8 — see
// features/stats/StatsScreen.tsx.
import { useTranslation } from 'react-i18next'

import { EmptyState } from '../ui'

function PlaceholderScreen({ titleKey }: { titleKey: 'links.browse' | 'links.settings' }) {
  const { t } = useTranslation('nav')

  return (
    <div className="flex min-h-[60vh] items-center justify-center p-6">
      <EmptyState title={t(titleKey)} description={t('comingSoon')} />
    </div>
  )
}

export function BrowseScreen() {
  return <PlaceholderScreen titleKey="links.browse" />
}

export function SettingsScreen() {
  return <PlaceholderScreen titleKey="links.settings" />
}
