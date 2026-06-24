import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { Field } from './Field'
import { TextInput } from './TextInput'

describe('Field', () => {
  it('associates help text with the control via aria-describedby', () => {
    render(
      <Field label="Email" help="We will never share this.">
        {(controlProps) => <TextInput {...controlProps} />}
      </Field>,
    )

    const input = screen.getByLabelText('Email')
    const help = screen.getByText('We will never share this.')

    expect(input).toHaveAttribute('aria-describedby', help.id)
    expect(input).not.toHaveAttribute('aria-invalid')
  })

  it('associates error text with the control via aria-describedby and sets aria-invalid', () => {
    render(
      <Field label="Email" error="Email is required" help="We will never share this.">
        {(controlProps) => <TextInput {...controlProps} />}
      </Field>,
    )

    const input = screen.getByLabelText('Email')
    const error = screen.getByRole('alert')

    expect(error).toHaveTextContent('Email is required')
    expect(input).toHaveAttribute('aria-describedby', error.id)
    expect(input).toHaveAttribute('aria-invalid', 'true')
    // Error replaces help text rather than both being shown.
    expect(screen.queryByText('We will never share this.')).not.toBeInTheDocument()
  })

  it('renders a required marker when required', () => {
    render(
      <Field label="Email" required>
        {(controlProps) => <TextInput {...controlProps} />}
      </Field>,
    )

    expect(screen.getByText('*')).toBeInTheDocument()
  })
})
