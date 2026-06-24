// Small control that cycles the theme mode light -> dark -> system -> light. Intentionally
// generic/unstyled-screen-agnostic (icon-less, text-label based) since this task builds the
// design-system foundation only — feature screens decide where/how to place it later.
import { useTranslation } from 'react-i18next'

import { useThemeStore } from './themeStore'

export function ThemeToggle() {
  const { t } = useTranslation('theme')
  const mode = useThemeStore((state) => state.mode)
  const cycle = useThemeStore((state) => state.cycle)

  const modeLabel = t(`modes.${mode}`)

  return (
    <button
      type="button"
      onClick={cycle}
      aria-label={t('toggleAriaLabel', { mode: modeLabel })}
      className="inline-flex items-center gap-2 rounded-md border border-border bg-surface px-3 py-1.5 text-sm text-fg transition-colors duration-[var(--duration-fast)] ease-default hover:bg-accent-muted"
    >
      <span aria-hidden="true" className="text-muted">
        {t('label')}
      </span>
      {modeLabel}
    </button>
  )
}
