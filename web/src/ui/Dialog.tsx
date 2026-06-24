// Accessible modal dialog: role="dialog" + aria-modal, focus trap, Esc-to-close, backdrop
// click-to-close, and focus restoration to the previously-focused element on close. Built on
// the native <dialog>-less pattern (a plain div) rather than HTMLDialogElement.showModal()
// because jsdom (the test environment) does not implement <dialog> modal behavior, and we
// need full control over focus-trap semantics anyway.
import { useEffect, useId, useRef } from 'react'
import type { KeyboardEvent, ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'

import { cn } from './cn'
import { IconButton } from './IconButton'
import { CloseIcon } from './icons'

export interface DialogProps {
  open: boolean
  onClose: () => void
  title: ReactNode
  children?: ReactNode
  /** Accessible label for the close icon button. Defaults to the translated `ui:dialog.close`. */
  closeLabel?: string
  className?: string
}

const FOCUSABLE_SELECTOR =
  'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])'

export function Dialog({ open, onClose, title, children, closeLabel, className }: DialogProps) {
  const { t } = useTranslation('ui')
  const resolvedCloseLabel = closeLabel ?? t('dialog.close')
  const reactId = useId()
  const titleId = `dialog-title-${reactId}`
  const containerRef = useRef<HTMLDivElement>(null)
  const previouslyFocused = useRef<HTMLElement | null>(null)

  useEffect(() => {
    if (!open) {
      return
    }

    previouslyFocused.current = document.activeElement as HTMLElement | null

    const container = containerRef.current
    const focusables = container?.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR)
    const first = focusables?.[0]
    first?.focus()

    return () => {
      previouslyFocused.current?.focus?.()
    }
  }, [open])

  if (!open) {
    return null
  }

  function handleKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (event.key === 'Escape') {
      event.stopPropagation()
      onClose()
      return
    }

    if (event.key !== 'Tab') {
      return
    }

    const container = containerRef.current
    if (!container) {
      return
    }
    const focusables = Array.from(container.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR))
    if (focusables.length === 0) {
      return
    }
    const first = focusables[0]
    const last = focusables[focusables.length - 1]
    const active = document.activeElement

    if (event.shiftKey && active === first) {
      event.preventDefault()
      last.focus()
    } else if (!event.shiftKey && active === last) {
      event.preventDefault()
      first.focus()
    }
  }

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-fg/40 p-4"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) {
          onClose()
        }
      }}
    >
      <div
        ref={containerRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onKeyDown={handleKeyDown}
        className={cn(
          'w-full max-w-md rounded-[var(--radius-lg)] border border-border bg-surface p-5 shadow-lg',
          'transition-opacity duration-[var(--duration-base)] ease-default',
          className,
        )}
      >
        <div className="mb-3 flex items-center justify-between gap-3">
          <h2 id={titleId} className="text-lg font-semibold text-fg">
            {title}
          </h2>
          <IconButton label={resolvedCloseLabel} icon={<CloseIcon />} size="sm" onClick={onClose} />
        </div>
        {children}
      </div>
    </div>,
    document.body,
  )
}
