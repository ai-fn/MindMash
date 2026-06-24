// Imperative toast queue, zustand-backed (matches the project's existing store convention,
// e.g. features/theme/themeStore.ts) so any code — deep in an event handler, outside React
// render — can call `toast(...)` without prop-drilling a dispatcher through the tree.
import { create } from 'zustand'

export type ToastTone = 'neutral' | 'success' | 'danger'

export interface ToastAction {
  label: string
  onClick: () => void
}

export interface ToastInput {
  title: string
  description?: string
  tone?: ToastTone
  action?: ToastAction
  /** Auto-dismiss after this many ms. Set to 0/undefined-with-action to require manual dismiss. */
  duration?: number
}

export interface ToastItem extends ToastInput {
  id: string
}

interface ToastState {
  toasts: ToastItem[]
  show: (input: ToastInput) => string
  dismiss: (id: string) => void
}

let counter = 0
function nextId(): string {
  counter += 1
  return `toast-${counter}`
}

export const useToastStore = create<ToastState>((set) => ({
  toasts: [],
  show(input) {
    const id = nextId()
    set((state) => ({ toasts: [...state.toasts, { ...input, id }] }))
    return id
  },
  dismiss(id) {
    set((state) => ({ toasts: state.toasts.filter((toast) => toast.id !== id) }))
  },
}))

/** Imperative helper for call sites that just want to fire a toast (e.g. after a mutation). */
export function toast(input: ToastInput): string {
  return useToastStore.getState().show(input)
}
