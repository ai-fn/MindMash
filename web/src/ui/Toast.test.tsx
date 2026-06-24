import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import '../lib/i18n.ts'
import { Toast } from './Toast'
import type { ToastItem } from './toastStore'

function makeToast(overrides: Partial<ToastItem> = {}): ToastItem {
  return {
    id: 'toast-1',
    title: 'Card deleted',
    ...overrides,
  }
}

describe('Toast', () => {
  it('renders the title and description', () => {
    render(<Toast toast={makeToast({ description: 'It has been removed from this deck.' })} onDismiss={vi.fn()} />)

    expect(screen.getByText('Card deleted')).toBeInTheDocument()
    expect(screen.getByText('It has been removed from this deck.')).toBeInTheDocument()
  })

  it('renders an action and fires its callback on click', async () => {
    const onClick = vi.fn()
    const user = userEvent.setup()
    render(
      <Toast
        toast={makeToast({ action: { label: 'Undo', onClick } })}
        onDismiss={vi.fn()}
      />,
    )

    const action = screen.getByRole('button', { name: 'Undo' })
    await user.click(action)

    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('calls onDismiss with the toast id when the dismiss button is clicked', async () => {
    const onDismiss = vi.fn()
    const user = userEvent.setup()
    render(<Toast toast={makeToast({ id: 'toast-42' })} onDismiss={onDismiss} />)

    await user.click(screen.getByRole('button', { name: 'Dismiss notification' }))

    expect(onDismiss).toHaveBeenCalledWith('toast-42')
  })

  it('does not render an action when none is provided', () => {
    render(<Toast toast={makeToast()} onDismiss={vi.fn()} />)

    expect(screen.queryByRole('button', { name: 'Undo' })).not.toBeInTheDocument()
  })
})
