import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import '../../lib/i18n.ts'

vi.mock('../../lib/api/collection/deck-service/deck-service', () => ({
  useDeckServiceDeleteDeck: vi.fn(),
  getDeckServiceListDecksQueryKey: vi.fn(() => ['/v1/decks']),
}))

const TOAST_MOCK = vi.fn()
vi.mock('../../ui', async () => {
  const actual = await vi.importActual<typeof import('../../ui')>('../../ui')
  return { ...actual, toast: TOAST_MOCK }
})

function makeDeck(overrides: Record<string, unknown> = {}) {
  return {
    id: 'deck-1',
    name: 'Spanish',
    usn: 'usn-5',
    ...overrides,
  }
}

async function renderDeleteDialog(
  deck: ReturnType<typeof makeDeck> | null = makeDeck(),
  onClose = vi.fn(),
) {
  const { DeleteDeckDialog } = await import('./DeleteDeckDialog.tsx')
  const queryClient = new QueryClient()
  const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
  render(
    <QueryClientProvider client={queryClient}>
      <DeleteDeckDialog deck={deck} onClose={onClose} />
    </QueryClientProvider>,
  )
  return { invalidateSpy }
}

describe('DeleteDeckDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.resetModules()
  })

  it('renders nothing when deck is null', async () => {
    const { useDeckServiceDeleteDeck } =
      await import('../../lib/api/collection/deck-service/deck-service')
    vi.mocked(useDeckServiceDeleteDeck).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
    } as never)

    await renderDeleteDialog(null)

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('shows the confirm copy with the deck name and does not delete until confirmed', async () => {
    const deleteMutate = vi.fn()
    const { useDeckServiceDeleteDeck } =
      await import('../../lib/api/collection/deck-service/deck-service')
    vi.mocked(useDeckServiceDeleteDeck).mockReturnValue({
      mutate: deleteMutate,
      isPending: false,
    } as never)

    await renderDeleteDialog(makeDeck({ name: 'Spanish' }))

    expect(screen.getByText('Delete "Spanish"? This can\'t be undone.')).toBeInTheDocument()
    expect(deleteMutate).not.toHaveBeenCalled()
  })

  it('does not call DeleteDeck when Cancel is clicked, and closes', async () => {
    const deleteMutate = vi.fn()
    const { useDeckServiceDeleteDeck } =
      await import('../../lib/api/collection/deck-service/deck-service')
    vi.mocked(useDeckServiceDeleteDeck).mockReturnValue({
      mutate: deleteMutate,
      isPending: false,
    } as never)
    const user = userEvent.setup()
    const onClose = vi.fn()

    await renderDeleteDialog(makeDeck(), onClose)
    await user.click(screen.getByRole('button', { name: 'Cancel' }))

    expect(deleteMutate).not.toHaveBeenCalled()
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('calls DeleteDeck with the fresh expectedUsn only after confirming, then invalidates and toasts', async () => {
    const onClose = vi.fn()
    const deleteMutate = vi.fn((_vars, opts: { onSuccess?: () => void }) => {
      opts.onSuccess?.()
    })
    const { useDeckServiceDeleteDeck } =
      await import('../../lib/api/collection/deck-service/deck-service')
    vi.mocked(useDeckServiceDeleteDeck).mockReturnValue({
      mutate: deleteMutate,
      isPending: false,
    } as never)
    const user = userEvent.setup()

    const { invalidateSpy } = await renderDeleteDialog(makeDeck({ usn: 'usn-42' }), onClose)
    await user.click(screen.getByRole('button', { name: 'Delete deck' }))

    expect(deleteMutate).toHaveBeenCalledWith(
      { id: 'deck-1', params: { expectedUsn: 'usn-42' } },
      expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
    )

    await waitFor(() => {
      expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['/v1/decks'] })
    })
    expect(onClose).toHaveBeenCalledTimes(1)
    expect(TOAST_MOCK).toHaveBeenCalledWith(
      expect.objectContaining({ title: 'Deck deleted.', tone: 'success' }),
    )
  })

  it('shows an error and stays open when DeleteDeck fails', async () => {
    const onClose = vi.fn()
    const deleteMutate = vi.fn((_vars, opts: { onError?: () => void }) => {
      opts.onError?.()
    })
    const { useDeckServiceDeleteDeck } =
      await import('../../lib/api/collection/deck-service/deck-service')
    vi.mocked(useDeckServiceDeleteDeck).mockReturnValue({
      mutate: deleteMutate,
      isPending: false,
    } as never)
    const user = userEvent.setup()

    await renderDeleteDialog(makeDeck(), onClose)
    await user.click(screen.getByRole('button', { name: 'Delete deck' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Something went wrong. Try again.')
    expect(onClose).not.toHaveBeenCalled()
    expect(TOAST_MOCK).not.toHaveBeenCalled()
  })
})
