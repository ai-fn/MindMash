import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { Spinner } from './Spinner'

describe('Spinner', () => {
  it('announces via role="status" with the given label', () => {
    render(<Spinner label="Loading cards" />)
    expect(screen.getByRole('status', { name: 'Loading cards' })).toBeInTheDocument()
  })

  it('is decorative (no status role, aria-hidden) when no label is passed', () => {
    const { container } = render(<Spinner />)
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
    expect(container.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  })

  it('applies the given pixel size', () => {
    render(<Spinner label="x" size={32} />)
    const status = screen.getByRole('status', { name: 'x' })
    expect(status).toHaveAttribute('width', '32')
    expect(status).toHaveAttribute('height', '32')
  })
})
