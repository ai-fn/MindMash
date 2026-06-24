// Empty-state placeholder: icon + message + an optional action slot (e.g. "Create your first
// deck" button). Copy/icon are always supplied by the caller via props — EmptyState owns no
// strings itself, since "no decks yet" vs "no results found" wording differs per screen.
import type { ReactNode } from 'react'

import { cn } from './cn'

export interface EmptyStateProps {
  icon?: ReactNode
  title: ReactNode
  description?: ReactNode
  action?: ReactNode
  className?: string
}

export function EmptyState({ icon, title, description, action, className }: EmptyStateProps) {
  return (
    <div className={cn('flex flex-col items-center gap-2 px-6 py-10 text-center', className)}>
      {icon ? (
        <div aria-hidden="true" className="mb-1 text-muted">
          {icon}
        </div>
      ) : null}
      <p className="text-base font-medium text-fg">{title}</p>
      {description ? <p className="max-w-sm text-sm text-muted">{description}</p> : null}
      {action ? <div className="mt-3">{action}</div> : null}
    </div>
  )
}
