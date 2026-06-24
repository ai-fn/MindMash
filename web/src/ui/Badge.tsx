// Small status label (e.g. card/deck state: "New", "Due", "Learning"). Tone maps onto the
// existing semantic tokens only — `neutral` (gray, default/info), `accent` (the single brand
// accent, for "in progress"-type states), `success`, `danger`. No new colors.
import type { HTMLAttributes } from 'react'

import { cn } from './cn'

export type BadgeTone = 'neutral' | 'accent' | 'success' | 'danger'

export interface BadgeProps extends HTMLAttributes<HTMLSpanElement> {
  tone?: BadgeTone
}

const toneClass: Record<BadgeTone, string> = {
  neutral: 'bg-border/60 text-muted',
  accent: 'bg-accent-muted text-accent',
  success: 'bg-success-muted text-success',
  danger: 'bg-danger-muted text-danger',
}

export function Badge({ tone = 'neutral', className, ...rest }: BadgeProps) {
  return (
    <span
      className={cn(
        'inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium',
        toneClass[tone],
        className,
      )}
      {...rest}
    />
  )
}
