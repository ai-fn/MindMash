import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import '../../lib/i18n.ts'

// Mock the generated deck-service hooks the same way DecksScreen.test.tsx mocks
// generated hooks, at the module path NewDeck imports them from.
vi.mock('../../lib/api/collection/deck-service/deck-service', () => ({
  useDeckServiceCreateDeck: vi.fn(),
  getDeckServiceListDecksQueryKey: vi.fn(() => ['/v1/decks']),
}))

async function renderNewDeck(onClose = vi.fn(), queryClient = new QueryClient()) {
  const { NewDeck } = await import('./NewDeck.tsx')
  return render(
    <QueryClientProvider client={queryClient}>
      <NewDeck open onClose={onClose} />
    </QueryClientProvider>,
  )
}

describe('NewDeck', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.resetModules()
  })

  it('renders nothing when closed', async () => {
    const { useDeckServiceCreateDeck } =
      await import('../../lib/api/collection/deck-service/deck-service')
    vi.mocked(useDeckServiceCreateDeck).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
    } as never)
    const { NewDeck } = await import('./NewDeck.tsx')

    render(
      <QueryClientProvider client={new QueryClient()}>
        <NewDeck open={false} onClose={vi.fn()} />
      </QueryClientProvider>,
    )

    expect(screen.queryByLabelText('New deck name')).not.toBeInTheDocument()
  })

  it('disables submit while the name input is empty', async () => {
    const { useDeckServiceCreateDeck } =
      await import('../../lib/api/collection/deck-service/deck-service')
    vi.mocked(useDeckServiceCreateDeck).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
    } as never)

    await renderNewDeck()

    expect(screen.getByRole('button', { name: 'Create deck' })).toBeDisabled()
  })

  it('enables submit once a name is entered, calls the mutation, clears the input, and closes on success', async () => {
    const { useDeckServiceCreateDeck } =
      await import('../../lib/api/collection/deck-service/deck-service')
    const mutate = vi.fn((_vars, opts: { onSuccess?: () => void }) => {
      opts.onSuccess?.()
    })
    vi.mocked(useDeckServiceCreateDeck).mockReturnValue({ mutate, isPending: false } as never)
    const user = userEvent.setup()
    const onClose = vi.fn()

    await renderNewDeck(onClose)
    const input = screen.getByLabelText('New deck name')
    await user.type(input, 'Spanish')

    expect(screen.getByRole('button', { name: 'Create deck' })).not.toBeDisabled()

    await user.click(screen.getByRole('button', { name: 'Create deck' }))

    expect(mutate).toHaveBeenCalledWith(
      { data: { name: 'Spanish' } },
      expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
    )
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('disables submit while the mutation is in-flight', async () => {
    const { useDeckServiceCreateDeck } =
      await import('../../lib/api/collection/deck-service/deck-service')
    vi.mocked(useDeckServiceCreateDeck).mockReturnValue({
      mutate: vi.fn(),
      isPending: true,
    } as never)
    const user = userEvent.setup()

    await renderNewDeck()
    await user.type(screen.getByLabelText('New deck name'), 'Spanish')

    expect(screen.getByRole('button', { name: 'Create deck' })).toBeDisabled()
  })

  it('shows an inline error on failure and does not close', async () => {
    const { useDeckServiceCreateDeck } =
      await import('../../lib/api/collection/deck-service/deck-service')
    const mutate = vi.fn((_vars, opts: { onError?: () => void }) => {
      opts.onError?.()
    })
    vi.mocked(useDeckServiceCreateDeck).mockReturnValue({ mutate, isPending: false } as never)
    const user = userEvent.setup()
    const onClose = vi.fn()

    await renderNewDeck(onClose)
    await user.type(screen.getByLabelText('New deck name'), 'Spanish')
    await user.click(screen.getByRole('button', { name: 'Create deck' }))

    expect(screen.getByText('Something went wrong. Try again.')).toBeInTheDocument()
    expect(onClose).not.toHaveBeenCalled()
  })
})
