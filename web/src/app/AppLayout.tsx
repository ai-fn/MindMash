// Minimal, quiet app shell (Constraint 2): a top bar with brand + nav links and the
// theme toggle, plus a skip-to-content link and the route <Outlet/>. Rendered for every
// authenticated route EXCEPT /study/:deckId, which stays full-bleed/bare (Constraint 1) —
// see router.tsx for the route split.
import { useTranslation } from 'react-i18next'
import { Link, NavLink, Outlet } from 'react-router-dom'

import { ThemeToggle } from '../features/theme/ThemeToggle'
import { cn } from '../ui'

const NAV_ITEMS = [
  { to: '/', key: 'decks' },
  { to: '/browse', key: 'browse' },
  { to: '/stats', key: 'stats' },
  { to: '/settings', key: 'settings' },
] as const

const navLinkBase =
  'rounded-[var(--radius-md)] px-3 py-1.5 text-sm font-medium transition-colors ' +
  'duration-[var(--duration-fast)] ease-default ' +
  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-bg'

export function AppLayout() {
  const { t: tNav } = useTranslation('nav')
  const { t: tCommon } = useTranslation('common')

  return (
    <div className="min-h-screen bg-bg">
      <a
        href="#main-content"
        className="sr-only focus:not-sr-only focus-visible:fixed focus-visible:top-2 focus-visible:left-2 focus-visible:z-50 focus-visible:rounded-[var(--radius-md)] focus-visible:bg-accent focus-visible:px-3 focus-visible:py-1.5 focus-visible:text-sm focus-visible:font-medium focus-visible:text-accent-fg"
      >
        {tNav('skipToContent')}
      </a>

      <header className="border-b border-border bg-surface">
        <div className="mx-auto flex flex-wrap items-center justify-between gap-3 px-4 py-3">
          <Link
            to="/"
            className="rounded-[var(--radius-md)] text-base font-semibold text-fg transition-colors duration-[var(--duration-fast)] ease-default hover:text-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-surface"
          >
            {tCommon('app.name')}
          </Link>

          <nav aria-label={tNav('primaryNavLabel')} className="flex flex-wrap items-center gap-1">
            {NAV_ITEMS.map((item) => (
              <NavLink
                key={item.key}
                to={item.to}
                end={item.to === '/'}
                className={({ isActive }) =>
                  cn(
                    navLinkBase,
                    isActive
                      ? 'bg-accent-muted text-fg'
                      : 'text-muted hover:bg-accent-muted hover:text-fg',
                  )
                }
              >
                {tNav(`links.${item.key}`)}
              </NavLink>
            ))}
          </nav>

          <ThemeToggle />
        </div>
      </header>

      <main id="main-content">
        <Outlet />
      </main>
    </div>
  )
}
