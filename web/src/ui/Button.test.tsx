import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { Button } from './Button'

describe('Button', () => {
  it.each([
    ['primary', 'bg-accent'],
    ['secondary', 'bg-surface'],
    ['ghost', 'bg-transparent'],
    ['danger', 'bg-danger'],
  ] as const)('renders the %s variant with its expected class', (variant, expectedClass) => {
    render(<Button variant={variant}>Click me</Button>)
    expect(screen.getByRole('button', { name: 'Click me' }).className).toContain(expectedClass)
  })

  it.each([
    ['sm', 'text-sm'],
    ['md', 'text-base'],
  ] as const)('renders the %s size with its expected class', (size, expectedClass) => {
    render(<Button size={size}>Click me</Button>)
    expect(screen.getByRole('button', { name: 'Click me' }).className).toContain(expectedClass)
  })

  it('fires onClick when enabled', async () => {
    const onClick = vi.fn()
    const user = userEvent.setup()
    render(<Button onClick={onClick}>Save</Button>)

    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('disables the button and blocks clicks when disabled', async () => {
    const onClick = vi.fn()
    const user = userEvent.setup()
    render(
      <Button onClick={onClick} disabled>
        Save
      </Button>,
    )

    const button = screen.getByRole('button', { name: 'Save' })
    expect(button).toBeDisabled()

    await user.click(button)
    expect(onClick).not.toHaveBeenCalled()
  })

  it('shows a spinner, marks aria-busy, and blocks clicks when loading', async () => {
    const onClick = vi.fn()
    const user = userEvent.setup()
    render(
      <Button onClick={onClick} loading>
        Save
      </Button>,
    )

    const button = screen.getByRole('button', { name: 'Save' })
    expect(button).toBeDisabled()
    expect(button).toHaveAttribute('aria-busy', 'true')
    expect(button.querySelector('svg[aria-hidden="true"]')).toBeInTheDocument()

    await user.click(button)
    expect(onClick).not.toHaveBeenCalled()
  })
})
