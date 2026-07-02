// Square, icon-only button (e.g. Dialog/Toast close affordances). Always requires an
// accessible label since there is no visible text — enforced via the required `label` prop
// rather than relying on every call site to remember `aria-label`.
import { forwardRef } from 'react'
import type { ButtonHTMLAttributes, ReactNode } from 'react'

import { cn, focusRing } from './cn'

export type IconButtonVariant = 'ghost' | 'secondary'
export type IconButtonSize = 'sm' | 'md'

export interface IconButtonProps extends Omit<
  ButtonHTMLAttributes<HTMLButtonElement>,
  'aria-label'
> {
  /** Required accessible name — this control has no visible text. */
  label: string
  icon: ReactNode
  variant?: IconButtonVariant
  size?: IconButtonSize
}

const base =
  'inline-flex items-center justify-center rounded-[var(--radius-md)] ' +
  'transition-colors duration-[var(--duration-fast)] ease-default ' +
  `${focusRing} focus-visible:ring-offset-2 focus-visible:ring-offset-bg ` +
  'disabled:cursor-not-allowed disabled:opacity-60'

const sizes: Record<IconButtonSize, string> = {
  sm: 'h-7 w-7 text-base',
  md: 'h-9 w-9 text-lg',
}

const variants: Record<IconButtonVariant, string> = {
  ghost: 'bg-transparent text-muted hover:bg-accent-muted hover:text-fg',
  secondary: 'bg-surface text-fg border border-border hover:bg-accent-muted',
}

export const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(function IconButton(
  { label, icon, variant = 'ghost', size = 'md', className, type = 'button', ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      aria-label={label}
      className={cn(base, sizes[size], variants[variant], className)}
      {...rest}
    >
      {icon}
    </button>
  )
})
