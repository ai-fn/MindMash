import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { Select } from './Select'

describe('Select', () => {
  it('renders its options and reflects the current value', () => {
    render(
      <Select value="b" onChange={() => {}} aria-label="Range">
        <option value="a">A</option>
        <option value="b">B</option>
      </Select>,
    )
    const select = screen.getByRole('combobox', { name: 'Range' }) as HTMLSelectElement
    expect(select.value).toBe('b')
    expect(screen.getByRole('option', { name: 'A' })).toBeInTheDocument()
  })

  it('forwards onChange', async () => {
    const onChange = vi.fn()
    const user = userEvent.setup()
    render(
      <Select value="a" onChange={onChange} aria-label="Range">
        <option value="a">A</option>
        <option value="b">B</option>
      </Select>,
    )
    await user.selectOptions(screen.getByRole('combobox', { name: 'Range' }), 'b')
    expect(onChange).toHaveBeenCalled()
  })

  it('applies invalid styling when invalid', () => {
    render(
      <Select aria-label="Range" invalid value="a" onChange={() => {}}>
        <option value="a">A</option>
      </Select>,
    )
    expect(screen.getByRole('combobox', { name: 'Range' }).className).toContain('border-danger')
  })
})
