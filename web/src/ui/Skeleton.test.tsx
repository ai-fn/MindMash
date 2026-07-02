import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { Skeleton } from './Skeleton'

describe('Skeleton', () => {
  it('renders a decorative (aria-hidden) pulsing placeholder', () => {
    const { container } = render(<Skeleton />)
    const el = container.firstChild as HTMLElement
    expect(el).toHaveAttribute('aria-hidden', 'true')
    expect(el.className).toContain('animate-pulse')
  })

  it('forwards className', () => {
    const { container } = render(<Skeleton className="h-10 w-full" />)
    expect((container.firstChild as HTMLElement).className).toContain('h-10')
  })
})
