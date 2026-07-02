import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { Switch } from './Switch'

describe('Switch', () => {
  it('renders as a switch with its label and description', () => {
    render(
      <Switch
        checked={false}
        onChange={() => {}}
        label="Advanced grading"
        description="Show 4 grades"
      />,
    )
    expect(screen.getByRole('switch', { name: /Advanced grading/ })).toBeInTheDocument()
    expect(screen.getByText('Show 4 grades')).toBeInTheDocument()
  })

  it('reflects the checked state via aria-checked', () => {
    const { rerender } = render(<Switch checked={false} onChange={() => {}} label="Adv" />)
    expect(screen.getByRole('switch')).toHaveAttribute('aria-checked', 'false')
    rerender(<Switch checked onChange={() => {}} label="Adv" />)
    expect(screen.getByRole('switch')).toHaveAttribute('aria-checked', 'true')
  })

  it('fires onChange when clicked', async () => {
    const onChange = vi.fn()
    const user = userEvent.setup()
    render(<Switch checked={false} onChange={onChange} label="Adv" />)
    await user.click(screen.getByRole('switch', { name: /Adv/ }))
    expect(onChange).toHaveBeenCalledOnce()
  })
})
