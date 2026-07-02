import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { IconButton } from './IconButton'

describe('IconButton', () => {
  it('exposes its required label as the accessible name and renders the icon', () => {
    render(<IconButton label="Close" icon={<svg data-testid="icon" />} />)
    expect(screen.getByRole('button', { name: 'Close' })).toBeInTheDocument()
    expect(screen.getByTestId('icon')).toBeInTheDocument()
  })

  it('defaults to type="button"', () => {
    render(<IconButton label="Close" icon={<svg />} />)
    expect(screen.getByRole('button', { name: 'Close' })).toHaveAttribute('type', 'button')
  })

  it('fires onClick when activated', async () => {
    const onClick = vi.fn()
    const user = userEvent.setup()
    render(<IconButton label="Close" icon={<svg />} onClick={onClick} />)
    await user.click(screen.getByRole('button', { name: 'Close' }))
    expect(onClick).toHaveBeenCalledOnce()
  })

  it('does not fire onClick when disabled', async () => {
    const onClick = vi.fn()
    const user = userEvent.setup()
    render(<IconButton label="Close" icon={<svg />} onClick={onClick} disabled />)
    await user.click(screen.getByRole('button', { name: 'Close' }))
    expect(onClick).not.toHaveBeenCalled()
  })
})
