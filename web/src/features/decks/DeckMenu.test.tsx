import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import '../../lib/i18n.ts'
import { DeckMenu } from './DeckMenu'

describe('DeckMenu', () => {
  it('is closed by default and opens the menu when the trigger is clicked', async () => {
    const user = userEvent.setup()
    render(<DeckMenu deckName="Spanish" onAddCard={vi.fn()} />)

    expect(screen.queryByRole('menu')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'More actions for Spanish' }))

    expect(screen.getByRole('menu', { name: 'More actions for Spanish' })).toBeInTheDocument()
  })

  it('calls onAddCard and closes the menu when "Add card" is clicked', async () => {
    const onAddCard = vi.fn()
    const user = userEvent.setup()
    render(<DeckMenu deckName="Spanish" onAddCard={onAddCard} />)

    await user.click(screen.getByRole('button', { name: 'More actions for Spanish' }))
    await user.click(screen.getByRole('menuitem', { name: 'Add card' }))

    expect(onAddCard).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  })

  it('renders Rename/Delete as disabled placeholders', async () => {
    const user = userEvent.setup()
    render(<DeckMenu deckName="Spanish" onAddCard={vi.fn()} />)

    await user.click(screen.getByRole('button', { name: 'More actions for Spanish' }))

    expect(screen.getByRole('menuitem', { name: 'Rename (coming soon)' })).toBeDisabled()
    expect(screen.getByRole('menuitem', { name: 'Delete (coming soon)' })).toBeDisabled()
  })

  it('closes when Escape is pressed', async () => {
    const user = userEvent.setup()
    render(<DeckMenu deckName="Spanish" onAddCard={vi.fn()} />)

    await user.click(screen.getByRole('button', { name: 'More actions for Spanish' }))
    expect(screen.getByRole('menu')).toBeInTheDocument()

    await user.keyboard('{Escape}')

    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  })

  it('closes when clicking outside the menu', async () => {
    const user = userEvent.setup()
    render(
      <div>
        <DeckMenu deckName="Spanish" onAddCard={vi.fn()} />
        <button type="button">Outside</button>
      </div>,
    )

    await user.click(screen.getByRole('button', { name: 'More actions for Spanish' }))
    expect(screen.getByRole('menu')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Outside' }))

    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  })
})
