import { forwardRef } from 'react'
import type { InputHTMLAttributes } from 'react'

import { cn, fieldControlClass } from './cn'

export interface TextInputProps extends InputHTMLAttributes<HTMLInputElement> {
  /** Marks the control as having a validation error — pairs with Field's error text. */
  invalid?: boolean
}

export const TextInput = forwardRef<HTMLInputElement, TextInputProps>(function TextInput(
  { invalid, className, ...rest },
  ref,
) {
  return (
    <input
      ref={ref}
      className={cn(fieldControlClass, invalid && 'border-danger focus-visible:ring-danger', className)}
      {...rest}
    />
  )
})
