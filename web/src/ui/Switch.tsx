// Labeled on/off toggle (e.g. Settings' advanced-grading preference). Renders as a single
// `role="switch"` button with the label/description as its accessible name/content, plus a
// decorative (aria-hidden) track+thumb that visually reflects `checked` — rather than a native
// `<input type="checkbox">`, so the whole row (label + description + track) is one click target
// and one tab stop.
import { cn } from './cn'

export interface SwitchProps {
  checked: boolean
  onChange: () => void
  label: string
  description?: string
  className?: string
}

export function Switch({ checked, onChange, label, description, className }: SwitchProps) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      onClick={onChange}
      className={cn(
        'flex items-center justify-between gap-4 rounded-[var(--radius-md)] border border-border bg-surface px-3 py-2 text-left transition-colors duration-[var(--duration-fast)] ease-default hover:bg-accent-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-bg',
        className,
      )}
    >
      <span className="flex flex-col gap-0.5">
        <span className="text-sm font-medium text-fg">{label}</span>
        {description ? <span className="text-sm text-muted">{description}</span> : null}
      </span>
      <span
        aria-hidden="true"
        className={`relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors duration-[var(--duration-fast)] ease-default ${
          checked ? 'bg-accent' : 'bg-border'
        }`}
      >
        <span
          className={`inline-block h-5 w-5 transform rounded-full bg-surface transition-transform duration-[var(--duration-fast)] ease-default ${
            checked ? 'translate-x-5' : 'translate-x-0.5'
          }`}
        />
      </span>
    </button>
  )
}
