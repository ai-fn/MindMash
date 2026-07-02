import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { EmptyState } from './EmptyState'

describe('EmptyState', () => {
  it('renders the title', () => {
    render(<EmptyState title="No decks yet" />)
    expect(screen.getByText('No decks yet')).toBeInTheDocument()
  })

  it('renders the optional description and action slots when provided', () => {
    render(
      <EmptyState
        title="No decks yet"
        description="Create your first deck."
        action={<button>Create deck</button>}
      />,
    )
    expect(screen.getByText('Create your first deck.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Create deck' })).toBeInTheDocument()
  })

  it('omits the action when none is provided', () => {
    render(<EmptyState title="Nothing here" />)
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })

  it('marks a provided icon decorative', () => {
    render(<EmptyState title="t" icon={<svg data-testid="icon" />} />)
    expect(screen.getByTestId('icon').parentElement).toHaveAttribute('aria-hidden', 'true')
  })
})
