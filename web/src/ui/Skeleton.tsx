// Loading placeholder block. Uses a CSS pulse animation (animation-duration, not
// transition-duration) — like Spinner, the global reduced-motion rule freezes the pulse
// rather than disabling it outright, which still communicates "loading" without judder.
import type { HTMLAttributes } from 'react'

import { cn } from './cn'

export type SkeletonProps = HTMLAttributes<HTMLDivElement>

export function Skeleton({ className, ...rest }: SkeletonProps) {
  return (
    <div
      aria-hidden="true"
      className={cn('animate-pulse rounded-[var(--radius-sm)] bg-border', className)}
      {...rest}
    />
  )
}
