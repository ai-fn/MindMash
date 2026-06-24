import { forwardRef } from 'react'
import type { TextareaHTMLAttributes } from 'react'

import { cn, fieldControlClass } from './cn'

export interface TextareaProps extends TextareaHTMLAttributes<HTMLTextAreaElement> {
  invalid?: boolean
}

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(function Textarea(
  { invalid, className, rows = 4, ...rest },
  ref,
) {
  return (
    <textarea
      ref={ref}
      rows={rows}
      className={cn(fieldControlClass, invalid && 'border-danger focus-visible:ring-danger', className)}
      {...rest}
    />
  )
})
