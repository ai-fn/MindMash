// Field: wires a label, a single form control, and optional help/error text together via
// generated ids + aria-describedby/aria-invalid. The control itself is passed as `children`
// (a render function) rather than Field rendering TextInput/Textarea/Select directly, so Field
// stays control-agnostic — it works with any of this package's form primitives, or a future
// one, without Field needing to know about each.
import { useId } from 'react'
import type { ReactNode } from 'react'

import { cn } from './cn'

export interface FieldControlProps {
  id: string
  'aria-describedby'?: string
  'aria-invalid'?: boolean
}

export interface FieldProps {
  label: ReactNode
  /** Help text shown when there is no error. Replaced by `error` when present. */
  help?: ReactNode
  error?: ReactNode
  required?: boolean
  className?: string
  /** Receives the wiring (id/aria-describedby/aria-invalid) to spread onto the control. */
  children: (controlProps: FieldControlProps) => ReactNode
}

export function Field({ label, help, error, required, className, children }: FieldProps) {
  const reactId = useId()
  const controlId = `field-${reactId}`
  const helpId = `${controlId}-help`
  const errorId = `${controlId}-error`

  const describedBy = error ? errorId : help ? helpId : undefined

  return (
    <div className={cn('flex flex-col gap-1.5', className)}>
      <label htmlFor={controlId} className="text-sm font-medium text-fg">
        {label}
        {required ? (
          <span aria-hidden="true" className="text-danger">
            {' '}
            *
          </span>
        ) : null}
      </label>
      {children({
        id: controlId,
        'aria-describedby': describedBy,
        'aria-invalid': error ? true : undefined,
      })}
      {error ? (
        <p id={errorId} role="alert" className="text-sm text-danger">
          {error}
        </p>
      ) : help ? (
        <p id={helpId} className="text-sm text-muted">
          {help}
        </p>
      ) : null}
    </div>
  )
}
