import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { Badge } from './Badge'

describe('Badge', () => {
  it('renders its children', () => {
    render(<Badge>New</Badge>)
    expect(screen.getByText('New')).toBeInTheDocument()
  })

  it('defaults to the neutral tone', () => {
    render(<Badge>New</Badge>)
    expect(screen.getByText('New').className).toContain('text-muted')
  })

  it('applies the semantic tone token classes', () => {
    render(<Badge tone="success">Review</Badge>)
    const el = screen.getByText('Review')
    expect(el.className).toContain('bg-success-muted')
    expect(el.className).toContain('text-success')
  })
})
