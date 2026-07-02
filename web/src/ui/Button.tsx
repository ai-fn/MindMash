// Primary interactive control. Variants encode semantic intent (primary action, secondary
// action, low-emphasis/ghost, destructive); all four reuse the existing design tokens only —
// no new colors. Sizes are deliberately limited to sm/md per the brief (no lg — none of the
// current screens need it, and a speculative size adds a variant nobody's tested).
import { forwardRef } from 'react'
import type { ButtonHTMLAttributes, ReactNode } from 'react'

import { cn, focusRing } from './cn'
import { Spinner } from './Spinner'

export type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger' | 'success'
export type ButtonSize = 'sm' | 'md'

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant
  size?: ButtonSize
  /** Shows a spinner in place of the leading content and disables interaction. */
  loading?: boolean
  /** Rendered after the label/spinner, e.g. a trailing icon. Decorative only. */
  trailing?: ReactNode
}

const base =
  'inline-flex items-center justify-center gap-2 rounded-[var(--radius-md)] font-medium ' +
  'transition-colors duration-[var(--duration-fast)] ease-default ' +
  `${focusRing} focus-visible:ring-offset-2 focus-visible:ring-offset-bg ` +
  'disabled:cursor-not-allowed disabled:opacity-60'

const sizes: Record<ButtonSize, string> = {
  sm: 'px-3 py-1.5 text-sm',
  md: 'px-4 py-2 text-base',
}

const variants: Record<ButtonVariant, string> = {
  primary: 'bg-accent text-accent-fg hover:brightness-110',
  secondary: 'bg-surface text-fg border border-border hover:bg-accent-muted',
  ghost: 'bg-transparent text-fg hover:bg-accent-muted',
  danger: 'bg-danger text-danger-fg hover:brightness-110',
  success: 'bg-success text-success-fg hover:brightness-110',
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  {
    variant = 'primary',
    size = 'md',
    loading = false,
    disabled = false,
    trailing,
    children,
    className,
    type = 'button',
    ...rest
  },
  ref,
) {
  const isDisabled = disabled || loading

  return (
    <button
      ref={ref}
      type={type}
      disabled={isDisabled}
      aria-busy={loading || undefined}
      className={cn(base, sizes[size], variants[variant], className)}
      {...rest}
    >
      {loading ? <Spinner size={size === 'sm' ? 14 : 16} label={null} /> : null}
      {children}
      {!loading && trailing ? trailing : null}
    </button>
  )
})
