// Indeterminate loading spinner. Pure CSS animation (spin), so it's exempt from the
// transition-duration-based reduced-motion rule in index.css — `animation-duration` IS
// covered by that rule (`*, *::before, *::after { animation-duration: 0.01ms !important }`),
// which effectively freezes the spin under prefers-reduced-motion. That's the correct
// behavior here (a frozen ring still conveys "loading" via aria-label, no judder).
import { cn } from './cn'

export interface SpinnerProps {
  /** Visual size in pixels. Defaults to 16 (inline with text/buttons). */
  size?: number
  className?: string
  /**
   * Accessible label, announced via role="status". Pass `null` when the spinner sits inside a
   * control that already announces its own busy state (e.g. Button's `aria-busy` + visible
   * label) — that avoids a redundant/duplicated accessible name on the parent control. There is
   * deliberately no English default here: this primitive owns no copy of its own (consistent
   * with EmptyState/Field), so every call site must pass a translated label or `null`.
   */
  label?: string | null
}

export function Spinner({ size = 16, className, label = null }: SpinnerProps) {
  return (
    <svg
      role={label ? 'status' : undefined}
      aria-label={label ?? undefined}
      aria-hidden={label ? undefined : 'true'}
      className={cn('inline-block animate-spin text-current', className)}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
    >
      <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="3" opacity="0.25" />
      <path d="M21 12a9 9 0 0 0-9-9" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
    </svg>
  )
}
