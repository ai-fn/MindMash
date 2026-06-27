import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import '../../lib/i18n.ts'

vi.mock('../../lib/api/collection/deck-service/deck-service', () => ({
  useDeckServiceUpdateDeck: vi.fn(),
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
    parentId: 'parent-1',
    configId: 'config-1',
    usn: 'usn-5',
    ...overrides,
  }
}

async function renderRenameDeck(
  deck: ReturnType<typeof makeDeck> | null = makeDeck(),
  onClose = vi.fn(),
) {
  const { RenameDeck } = await import('./RenameDeck.tsx')
  const queryClient = new QueryClient()
  const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
  render(
    <QueryClientProvider client={queryClient}>
      <RenameDeck deck={deck} onClose={onClose} />
    </QueryClientProvider>,
  )
  return { invalidateSpy }
}

describe('RenameDeck', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.resetModules()
  })

  it('renders nothing when deck is null', async () => {
    const { useDeckServiceUpdateDeck } =
      await import('../../lib/api/collection/deck-service/deck-service')
    vi.mocked(useDeckServiceUpdateDeck).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
    } as never)

    await renderRenameDeck(null)

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('pre-fills the input with the deck name', async () => {
    const { useDeckServiceUpdateDeck } =
      await import('../../lib/api/collection/deck-service/deck-service')
    vi.mocked(useDeckServiceUpdateDeck).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
    } as never)

    await renderRenameDeck()

    expect(screen.getByDisplayValue('Spanish')).toBeInTheDocument()
  })

  it('submits UpdateDeck with the edited name, preserved parentId/configId, and the fresh expectedUsn', async () => {
    const mutate = vi.fn((_vars, opts: { onSuccess?: () => void }) => {
      opts.onSuccess?.()
    })
    const { useDeckServiceUpdateDeck } =
      await import('../../lib/api/collection/deck-service/deck-service')
    vi.mocked(useDeckServiceUpdateDeck).mockReturnValue({ mutate, isPending: false } as never)
    const user = userEvent.setup()
    const onClose = vi.fn()

    const { invalidateSpy } = await renderRenameDeck(makeDeck({ usn: 'usn-9' }), onClose)

    const input = screen.getByDisplayValue('Spanish')
    await user.clear(input)
    await user.type(input, 'Español')
    await user.click(screen.getByRole('button', { name: 'Save name' }))

    expect(mutate).toHaveBeenCalledWith(
      {
        id: 'deck-1',
        data: {
          name: 'Español',
          parentId: 'parent-1',
          configId: 'config-1',
          expectedUsn: 'usn-9',
        },
      },
      expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
    )

    await waitFor(() => {
      expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['/v1/decks'] })
    })
    expect(onClose).toHaveBeenCalledTimes(1)
    expect(TOAST_MOCK).toHaveBeenCalledWith(
      expect.objectContaining({ title: 'Deck renamed.', tone: 'success' }),
    )
  })

  it('shows an inline error and does not close on failure', async () => {
    const mutate = vi.fn((_vars, opts: { onError?: () => void }) => {
      opts.onError?.()
    })
    const { useDeckServiceUpdateDeck } =
      await import('../../lib/api/collection/deck-service/deck-service')
    vi.mocked(useDeckServiceUpdateDeck).mockReturnValue({ mutate, isPending: false } as never)
    const user = userEvent.setup()
    const onClose = vi.fn()

    await renderRenameDeck(makeDeck(), onClose)
    await user.click(screen.getByRole('button', { name: 'Save name' }))

    expect(screen.getByText('Something went wrong. Try again.')).toBeInTheDocument()
    expect(onClose).not.toHaveBeenCalled()
    expect(TOAST_MOCK).not.toHaveBeenCalled()
  })

  it('disables submit while the name is empty or the mutation is in-flight', async () => {
    const { useDeckServiceUpdateDeck } =
      await import('../../lib/api/collection/deck-service/deck-service')
    vi.mocked(useDeckServiceUpdateDeck).mockReturnValue({
      mutate: vi.fn(),
      isPending: true,
    } as never)

    await renderRenameDeck()

    expect(screen.getByRole('button', { name: 'Save name' })).toBeDisabled()
  })
})
