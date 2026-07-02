import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { Card, Surface } from './Card'

describe('Surface', () => {
  it('renders children in a token-driven box, bordered by default', () => {
    const { container } = render(
      <Surface>
        <span>content</span>
      </Surface>,
    )
    const el = container.firstChild as HTMLElement
    expect(screen.getByText('content')).toBeInTheDocument()
    expect(el.className).toContain('bg-surface')
    expect(el.className).toContain('border-border')
  })

  it('omits the border when bordered={false}', () => {
    const { container } = render(<Surface bordered={false} />)
    expect((container.firstChild as HTMLElement).className).not.toContain('border-border')
  })

  it('forwards className and arbitrary props', () => {
    const { container } = render(<Surface className="custom" id="s1" />)
    const el = container.firstChild as HTMLElement
    expect(el.className).toContain('custom')
    expect(el).toHaveAttribute('id', 's1')
  })
})

describe('Card', () => {
  it('is a Surface with standard padding', () => {
    const { container } = render(<Card>body</Card>)
    const el = container.firstChild as HTMLElement
    expect(el.className).toContain('p-4')
    expect(el.className).toContain('bg-surface')
    expect(screen.getByText('body')).toBeInTheDocument()
  })
})
