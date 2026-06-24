// Surface/Card: the basic content-grouping container. `Surface` is the bare token-driven
// box (bg/border/radius only); `Card` adds the standard padding so most call sites just use
// `Card` while layouts that need bespoke padding can drop to `Surface`.
import type { HTMLAttributes } from 'react'

import { cn } from './cn'

export interface SurfaceProps extends HTMLAttributes<HTMLDivElement> {
  /** Adds a visible border. Defaults on — most surfaces in this design sit on a flat bg. */
  bordered?: boolean
}

export function Surface({ bordered = true, className, ...rest }: SurfaceProps) {
  return (
    <div
      className={cn(
        'rounded-[var(--radius-lg)] bg-surface',
        bordered && 'border border-border',
        className,
      )}
      {...rest}
    />
  )
}

export type CardProps = SurfaceProps

export function Card({ className, ...rest }: CardProps) {
  return <Surface className={cn('p-4', className)} {...rest} />
}
