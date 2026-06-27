import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import '../../lib/i18n.ts'
import { DeckMenu } from './DeckMenu'

function renderMenu(overrides: Partial<Parameters<typeof DeckMenu>[0]> = {}) {
  const props = {
    deckName: 'Spanish',
    onAddCard: vi.fn(),
    onRename: vi.fn(),
    onDelete: vi.fn(),
    onCreateSubdeck: vi.fn(),
    onEditConfig: vi.fn(),
    ...overrides,
  }
  render(<DeckMenu {...props} />)
  return props
}

describe('DeckMenu', () => {
  it('is closed by default and opens the menu when the trigger is clicked', async () => {
    const user = userEvent.setup()
    renderMenu()

    expect(screen.queryByRole('menu')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'More actions for Spanish' }))

    expect(screen.getByRole('menu', { name: 'More actions for Spanish' })).toBeInTheDocument()
  })

  it('calls onAddCard and closes the menu when "Add card" is clicked', async () => {
    const user = userEvent.setup()
    const { onAddCard } = renderMenu()

    await user.click(screen.getByRole('button', { name: 'More actions for Spanish' }))
    await user.click(screen.getByRole('menuitem', { name: 'Add card' }))

    expect(onAddCard).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  })

  it('calls onCreateSubdeck and closes the menu when "Create subdeck" is clicked', async () => {
    const user = userEvent.setup()
    const { onCreateSubdeck } = renderMenu()

    await user.click(screen.getByRole('button', { name: 'More actions for Spanish' }))
    await user.click(screen.getByRole('menuitem', { name: 'Create subdeck' }))

    expect(onCreateSubdeck).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  })

  it('calls onEditConfig and closes the menu when "Deck settings" is clicked', async () => {
    const user = userEvent.setup()
    const { onEditConfig } = renderMenu()

    await user.click(screen.getByRole('button', { name: 'More actions for Spanish' }))
    await user.click(screen.getByRole('menuitem', { name: 'Deck settings' }))

    expect(onEditConfig).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  })

  it('calls onRename and closes the menu when "Rename" is clicked', async () => {
    const user = userEvent.setup()
    const { onRename } = renderMenu()

    await user.click(screen.getByRole('button', { name: 'More actions for Spanish' }))
    await user.click(screen.getByRole('menuitem', { name: 'Rename' }))

    expect(onRename).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  })

  it('calls onDelete and closes the menu when "Delete" is clicked', async () => {
    const user = userEvent.setup()
    const { onDelete } = renderMenu()

    await user.click(screen.getByRole('button', { name: 'More actions for Spanish' }))
    await user.click(screen.getByRole('menuitem', { name: 'Delete' }))

    expect(onDelete).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  })

  it('closes when Escape is pressed', async () => {
    const user = userEvent.setup()
    renderMenu()

    await user.click(screen.getByRole('button', { name: 'More actions for Spanish' }))
    expect(screen.getByRole('menu')).toBeInTheDocument()

    await user.keyboard('{Escape}')

    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  })

  it('closes when clicking outside the menu', async () => {
    const user = userEvent.setup()
    renderMenu()
    render(<button type="button">Outside</button>)

    await user.click(screen.getByRole('button', { name: 'More actions for Spanish' }))
    expect(screen.getByRole('menu')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Outside' }))

    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  })
})
