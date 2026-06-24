import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import '../lib/i18n.ts'
import { Dialog } from './Dialog'

describe('Dialog', () => {
  it('renders with correct ARIA attributes when open', () => {
    render(
      <Dialog open onClose={vi.fn()} title="Delete deck">
        <p>Are you sure?</p>
      </Dialog>,
    )

    const dialog = screen.getByRole('dialog')
    expect(dialog).toHaveAttribute('aria-modal', 'true')
    expect(dialog).toHaveAccessibleName('Delete deck')
  })

  it('renders nothing when closed', () => {
    render(
      <Dialog open={false} onClose={vi.fn()} title="Delete deck">
        <p>Are you sure?</p>
      </Dialog>,
    )

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('calls onClose on Escape', async () => {
    const onClose = vi.fn()
    const user = userEvent.setup()
    render(
      <Dialog open onClose={onClose} title="Delete deck">
        <p>Are you sure?</p>
      </Dialog>,
    )

    await user.keyboard('{Escape}')
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('calls onClose on backdrop click but not on dialog content click', async () => {
    const onClose = vi.fn()
    const user = userEvent.setup()
    render(
      <Dialog open onClose={onClose} title="Delete deck">
        <p>Are you sure?</p>
      </Dialog>,
    )

    await user.click(screen.getByText('Are you sure?'))
    expect(onClose).not.toHaveBeenCalled()

    // The backdrop is the dialog role's parent (currentTarget of the mousedown handler).
    const backdrop = screen.getByRole('dialog').parentElement as HTMLElement
    await user.click(backdrop)
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('traps focus within the dialog when tabbing past the last element', async () => {
    const onClose = vi.fn()
    const user = userEvent.setup()
    render(
      <div>
        <button type="button">Outside</button>
        <Dialog open onClose={onClose} title="Delete deck">
          <button type="button">Confirm</button>
          <button type="button">Cancel</button>
        </Dialog>
      </div>,
    )

    const closeButton = screen.getByRole('button', { name: 'Close' })
    const confirmButton = screen.getByRole('button', { name: 'Confirm' })
    const cancelButton = screen.getByRole('button', { name: 'Cancel' })

    // Initial focus goes to the first focusable element inside the dialog.
    expect(closeButton).toHaveFocus()

    cancelButton.focus()
    await user.tab()
    expect(closeButton).toHaveFocus()

    closeButton.focus()
    await user.tab({ shift: true })
    expect(cancelButton).toHaveFocus()

    // Sanity: focus never lands on the outside trigger while open.
    expect(confirmButton).not.toHaveFocus()
  })

  it('restores focus to the previously-focused element on close', () => {
    const onClose = vi.fn()
    function Wrapper({ open }: { open: boolean }) {
      return (
        <div>
          <button type="button">Outside trigger</button>
          <Dialog open={open} onClose={onClose} title="Delete deck">
            <button type="button">Confirm</button>
          </Dialog>
        </div>
      )
    }

    const { rerender } = render(<Wrapper open={false} />)
    const trigger = screen.getByRole('button', { name: 'Outside trigger' })
    trigger.focus()
    expect(trigger).toHaveFocus()

    rerender(<Wrapper open />)
    expect(trigger).not.toHaveFocus()

    rerender(<Wrapper open={false} />)
    expect(trigger).toHaveFocus()
  })
})
