// Native <select> styled to match TextInput/Textarea, with a decorative chevron overlay
// (native selects already have an OS-drawn arrow on some platforms, but it's inconsistent
// across browsers/OSes — drawing our own keeps it token-colored and consistent everywhere).
// Native semantics are kept deliberately (no custom listbox) — it's free keyboard/a11y
// behavior and this design doesn't need multi-select or rich option rendering.
import { forwardRef } from 'react'
import type { SelectHTMLAttributes } from 'react'

import { cn, fieldControlClass } from './cn'
import { ChevronDownIcon } from './icons'

export interface SelectProps extends SelectHTMLAttributes<HTMLSelectElement> {
  invalid?: boolean
}

export const Select = forwardRef<HTMLSelectElement, SelectProps>(function Select(
  { invalid, className, children, ...rest },
  ref,
) {
  return (
    <div className="relative">
      <select
        ref={ref}
        className={cn(
          fieldControlClass,
          'appearance-none pr-9',
          invalid && 'border-danger focus-visible:ring-danger',
          className,
        )}
        {...rest}
      >
        {children}
      </select>
      <ChevronDownIcon className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-muted" />
    </div>
  )
})
