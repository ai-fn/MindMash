import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import '../lib/i18n.ts'
import { App } from './App.tsx'

describe('App', () => {
  it('renders the translated app heading', () => {
    render(<App />)
    expect(screen.getByRole('heading', { name: 'MindMesh' })).toBeInTheDocument()
  })
})
