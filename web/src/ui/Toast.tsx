// Toast (single notification) + Toaster (the viewport that renders the live queue from
// toastStore). Toast accepts an explicit `onDismiss` so it's usable standalone/in tests
// without the store; Toaster wires it to toastStore for app-wide use and handles
// auto-dismiss timers.
import { useEffect } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'

import { cn, focusRing } from './cn'
import { IconButton } from './IconButton'
import { CloseIcon } from './icons'
import type { ToastItem, ToastTone } from './toastStore'
import { useToastStore } from './toastStore'

const DEFAULT_DURATION_MS = 5000

const toneClass: Record<ToastTone, string> = {
  neutral: 'border-border',
  success: 'border-success',
  danger: 'border-danger',
}

export interface ToastProps {
  toast: ToastItem
  onDismiss: (id: string) => void
}

export function Toast({ toast, onDismiss }: ToastProps) {
  const { t } = useTranslation('ui')
  const tone = toast.tone ?? 'neutral'

  return (
    <div
      role="status"
      className={cn(
        'flex items-start gap-3 rounded-[var(--radius-md)] border-l-4 bg-surface p-3 shadow-md',
        'border border-border',
        toneClass[tone],
      )}
    >
      <div className="min-w-0 flex-1">
        <p className="text-sm font-medium text-fg">{toast.title}</p>
        {toast.description ? (
          <p className="mt-0.5 text-sm text-muted">{toast.description}</p>
        ) : null}
        {toast.action ? (
          <button
            type="button"
            onClick={toast.action.onClick}
            className={cn(
              'mt-2 text-sm font-medium text-accent underline-offset-2 hover:underline',
              `${focusRing} focus-visible:rounded-sm`,
            )}
          >
            {toast.action.label}
          </button>
        ) : null}
      </div>
      <IconButton
        label={t('toast.dismiss')}
        icon={<CloseIcon />}
        size="sm"
        onClick={() => onDismiss(toast.id)}
      />
    </div>
  )
}

export function Toaster() {
  const toasts = useToastStore((state) => state.toasts)
  const dismiss = useToastStore((state) => state.dismiss)

  return createPortal(
    <div className="fixed bottom-4 right-4 z-50 flex w-full max-w-sm flex-col gap-2">
      {toasts.map((item) => (
        <ToastAutoDismiss key={item.id} toast={item} onDismiss={dismiss} />
      ))}
    </div>,
    document.body,
  )
}

function ToastAutoDismiss({ toast: item, onDismiss }: ToastProps) {
  useEffect(() => {
    const duration = item.duration ?? DEFAULT_DURATION_MS
    if (duration <= 0) {
      return
    }
    const timer = window.setTimeout(() => onDismiss(item.id), duration)
    return () => window.clearTimeout(timer)
  }, [item.id, item.duration, onDismiss])

  return <Toast toast={item} onDismiss={onDismiss} />
}
