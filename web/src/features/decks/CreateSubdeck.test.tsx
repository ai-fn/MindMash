import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import '../../lib/i18n.ts'

vi.mock('../../lib/api/collection/deck-service/deck-service', () => ({
  useDeckServiceCreateDeck: vi.fn(),
  getDeckServiceListDecksQueryKey: vi.fn(() => ['/v1/decks']),
}))

const TOAST_MOCK = vi.fn()
vi.mock('../../ui', async () => {
  const actual = await vi.importActual<typeof import('../../ui')>('../../ui')
  return { ...actual, toast: TOAST_MOCK }
})

async function renderCreateSubdeck(
  parentDeckId: string | null = 'parent-1',
  parentDeckName = 'Spanish',
  onClose = vi.fn(),
) {
  const { CreateSubdeck } = await import('./CreateSubdeck.tsx')
  const queryClient = new QueryClient()
  const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
  render(
    <QueryClientProvider client={queryClient}>
      <CreateSubdeck
        parentDeckId={parentDeckId}
        parentDeckName={parentDeckName}
        onClose={onClose}
      />
    </QueryClientProvider>,
  )
  return { invalidateSpy }
}

describe('CreateSubdeck', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.resetModules()
  })

  it('renders nothing when parentDeckId is null', async () => {
    const { useDeckServiceCreateDeck } =
      await import('../../lib/api/collection/deck-service/deck-service')
    vi.mocked(useDeckServiceCreateDeck).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
    } as never)

    await renderCreateSubdeck(null)

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('shows the parent deck name in the dialog title', async () => {
    const { useDeckServiceCreateDeck } =
      await import('../../lib/api/collection/deck-service/deck-service')
    vi.mocked(useDeckServiceCreateDeck).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
    } as never)

    await renderCreateSubdeck('parent-1', 'Spanish')

    expect(screen.getByText('Create a subdeck of "Spanish"')).toBeInTheDocument()
  })

  it('submits CreateDeck with the entered name and the parent deck id as parentId', async () => {
    const mutate = vi.fn((_vars, opts: { onSuccess?: () => void }) => {
      opts.onSuccess?.()
    })
    const { useDeckServiceCreateDeck } =
      await import('../../lib/api/collection/deck-service/deck-service')
    vi.mocked(useDeckServiceCreateDeck).mockReturnValue({ mutate, isPending: false } as never)
    const user = userEvent.setup()
    const onClose = vi.fn()

    const { invalidateSpy } = await renderCreateSubdeck('parent-1', 'Spanish', onClose)

    await user.type(screen.getByLabelText('New subdeck name'), 'Verbs')
    await user.click(screen.getByRole('button', { name: 'Create subdeck' }))

    expect(mutate).toHaveBeenCalledWith(
      { data: { name: 'Verbs', parentId: 'parent-1' } },
      expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
    )

    await waitFor(() => {
      expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['/v1/decks'] })
    })
    expect(onClose).toHaveBeenCalledTimes(1)
    expect(TOAST_MOCK).toHaveBeenCalledWith(
      expect.objectContaining({ title: 'Subdeck created.', tone: 'success' }),
    )
  })

  it('disables submit while the name is empty', async () => {
    const { useDeckServiceCreateDeck } =
      await import('../../lib/api/collection/deck-service/deck-service')
    vi.mocked(useDeckServiceCreateDeck).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
    } as never)

    await renderCreateSubdeck()

    expect(screen.getByRole('button', { name: 'Create subdeck' })).toBeDisabled()
  })

  it('shows an inline error and does not close on failure', async () => {
    const mutate = vi.fn((_vars, opts: { onError?: () => void }) => {
      opts.onError?.()
    })
    const { useDeckServiceCreateDeck } =
      await import('../../lib/api/collection/deck-service/deck-service')
    vi.mocked(useDeckServiceCreateDeck).mockReturnValue({ mutate, isPending: false } as never)
    const user = userEvent.setup()
    const onClose = vi.fn()

    await renderCreateSubdeck('parent-1', 'Spanish', onClose)
    await user.type(screen.getByLabelText('New subdeck name'), 'Verbs')
    await user.click(screen.getByRole('button', { name: 'Create subdeck' }))

    expect(screen.getByText('Something went wrong. Try again.')).toBeInTheDocument()
    expect(onClose).not.toHaveBeenCalled()
  })
})
