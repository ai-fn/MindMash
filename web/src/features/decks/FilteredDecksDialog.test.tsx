import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import '../../lib/i18n.ts'

vi.mock('../../lib/api/collection/filtered-deck-service/filtered-deck-service', () => ({
  useFilteredDeckServiceListFilteredDecks: vi.fn(),
  useFilteredDeckServiceCreateFilteredDeck: vi.fn(),
  useFilteredDeckServiceDeleteFilteredDeck: vi.fn(),
  getFilteredDeckServiceListFilteredDecksQueryKey: vi.fn(() => ['/v1/filtered-decks']),
}))

const TOAST_MOCK = vi.fn()
vi.mock('../../ui', async () => {
  const actual = await vi.importActual<typeof import('../../ui')>('../../ui')
  return { ...actual, toast: TOAST_MOCK }
})

function makeFilteredDeck(overrides: Record<string, unknown> = {}) {
  return {
    id: 'fd-1',
    name: 'Due Spanish',
    query: 'deck:Spanish is:due',
    usn: 'usn-fd-1',
    ...overrides,
  }
}

async function setupHooks({
  items = [] as ReturnType<typeof makeFilteredDeck>[],
  listPending = false,
  listError = false,
  createMutate = vi.fn(),
  createPending = false,
  deleteMutate = vi.fn(),
  deletePending = false,
}: {
  items?: ReturnType<typeof makeFilteredDeck>[]
  listPending?: boolean
  listError?: boolean
  createMutate?: ReturnType<typeof vi.fn>
  createPending?: boolean
  deleteMutate?: ReturnType<typeof vi.fn>
  deletePending?: boolean
} = {}) {
  const {
    useFilteredDeckServiceListFilteredDecks,
    useFilteredDeckServiceCreateFilteredDeck,
    useFilteredDeckServiceDeleteFilteredDeck,
  } = await import('../../lib/api/collection/filtered-deck-service/filtered-deck-service')

  vi.mocked(useFilteredDeckServiceListFilteredDecks).mockReturnValue({
    data: listError ? undefined : { data: { items } },
    isPending: listPending,
    isError: listError,
  } as never)

  vi.mocked(useFilteredDeckServiceCreateFilteredDeck).mockReturnValue({
    mutate: createMutate,
    isPending: createPending,
  } as never)

  vi.mocked(useFilteredDeckServiceDeleteFilteredDeck).mockReturnValue({
    mutate: deleteMutate,
    isPending: deletePending,
  } as never)

  return { createMutate, deleteMutate }
}

async function renderDialog(open = true, onClose = vi.fn()) {
  const { FilteredDecksDialog } = await import('./FilteredDecksDialog.tsx')
  const queryClient = new QueryClient()
  const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
  render(
    <QueryClientProvider client={queryClient}>
      <FilteredDecksDialog open={open} onClose={onClose} />
    </QueryClientProvider>,
  )
  return { invalidateSpy }
}

describe('FilteredDecksDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.resetModules()
  })

  it('renders nothing when closed', async () => {
    await setupHooks()
    await renderDialog(false)

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('shows a loading state while the list is pending', async () => {
    await setupHooks({ listPending: true })
    await renderDialog()

    expect(screen.getByRole('status', { name: 'Loading filtered decks…' })).toBeInTheDocument()
  })

  it('shows an error state when the list fails', async () => {
    await setupHooks({ listError: true })
    await renderDialog()

    expect(screen.getByRole('alert')).toHaveTextContent('Something went wrong. Try again.')
  })

  it('shows an empty state when there are no filtered decks', async () => {
    await setupHooks({ items: [] })
    await renderDialog()

    expect(screen.getByText('No filtered decks yet.')).toBeInTheDocument()
  })

  it('renders each filtered deck with its name and query', async () => {
    await setupHooks({
      items: [makeFilteredDeck({ id: 'fd-1', name: 'Due Spanish', query: 'deck:Spanish is:due' })],
    })
    await renderDialog()

    expect(screen.getByText('Due Spanish')).toBeInTheDocument()
    expect(screen.getByText('deck:Spanish is:due')).toBeInTheDocument()
  })

  it('does not submit while name or query is blank, and submits CreateFilteredDeck once both are filled', async () => {
    const createMutate = vi.fn((_vars, opts: { onSuccess?: () => void }) => {
      opts.onSuccess?.()
    })
    const { invalidateSpy } = await (async () => {
      await setupHooks({ createMutate })
      return renderDialog()
    })()
    const user = userEvent.setup()

    expect(screen.getByRole('button', { name: 'Create filtered deck' })).toBeDisabled()

    await user.type(screen.getByLabelText('Name'), 'Due Spanish')
    await user.type(screen.getByLabelText('Query'), 'deck:Spanish is:due')

    const submit = screen.getByRole('button', { name: 'Create filtered deck' })
    expect(submit).toBeEnabled()
    await user.click(submit)

    expect(createMutate).toHaveBeenCalledWith(
      { data: { name: 'Due Spanish', query: 'deck:Spanish is:due' } },
      expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
    )

    await waitFor(() => {
      expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['/v1/filtered-decks'] })
    })
    expect(TOAST_MOCK).toHaveBeenCalledWith(
      expect.objectContaining({ title: 'Filtered deck created.', tone: 'success' }),
    )
    expect(screen.getByLabelText('Name')).toHaveValue('')
    expect(screen.getByLabelText('Query')).toHaveValue('')
  })

  it('shows an error and keeps the form filled when CreateFilteredDeck fails', async () => {
    const createMutate = vi.fn((_vars, opts: { onError?: () => void }) => {
      opts.onError?.()
    })
    await setupHooks({ createMutate })
    const user = userEvent.setup()

    await renderDialog()
    await user.type(screen.getByLabelText('Name'), 'Due Spanish')
    await user.type(screen.getByLabelText('Query'), 'deck:Spanish is:due')
    await user.click(screen.getByRole('button', { name: 'Create filtered deck' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Something went wrong. Try again.')
    expect(TOAST_MOCK).not.toHaveBeenCalled()
    expect(screen.getByLabelText('Name')).toHaveValue('Due Spanish')
  })

  it('requires a two-step confirm and does not delete until confirmed', async () => {
    const deleteMutate = vi.fn()
    await setupHooks({ items: [makeFilteredDeck()], deleteMutate })
    const user = userEvent.setup()

    await renderDialog()
    await user.click(screen.getByRole('button', { name: 'Delete' }))

    expect(screen.getByText('Delete "Due Spanish"? This can\'t be undone.')).toBeInTheDocument()
    expect(deleteMutate).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: 'Cancel' }))

    expect(
      screen.queryByText('Delete "Due Spanish"? This can\'t be undone.'),
    ).not.toBeInTheDocument()
    expect(deleteMutate).not.toHaveBeenCalled()
  })

  it('calls DeleteFilteredDeck with the fresh expectedUsn read live from list data, then invalidates and toasts', async () => {
    const deleteMutate = vi.fn((_vars, opts: { onSuccess?: () => void }) => {
      opts.onSuccess?.()
    })
    const { invalidateSpy } = await (async () => {
      await setupHooks({
        items: [makeFilteredDeck({ id: 'fd-1', name: 'Due Spanish', usn: 'usn-fd-42' })],
        deleteMutate,
      })
      return renderDialog()
    })()
    const user = userEvent.setup()

    await user.click(screen.getByRole('button', { name: 'Delete' }))
    await user.click(screen.getByRole('button', { name: 'Delete filtered deck' }))

    expect(deleteMutate).toHaveBeenCalledWith(
      { id: 'fd-1', params: { expectedUsn: 'usn-fd-42' } },
      expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
    )

    await waitFor(() => {
      expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['/v1/filtered-decks'] })
    })
    expect(TOAST_MOCK).toHaveBeenCalledWith(
      expect.objectContaining({ title: 'Filtered deck deleted.', tone: 'success' }),
    )
  })

  it('shows an error and keeps the confirm step open when DeleteFilteredDeck fails', async () => {
    const deleteMutate = vi.fn((_vars, opts: { onError?: () => void }) => {
      opts.onError?.()
    })
    await setupHooks({ items: [makeFilteredDeck()], deleteMutate })
    const user = userEvent.setup()

    await renderDialog()
    await user.click(screen.getByRole('button', { name: 'Delete' }))
    await user.click(screen.getByRole('button', { name: 'Delete filtered deck' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Something went wrong. Try again.')
    expect(TOAST_MOCK).not.toHaveBeenCalled()
    expect(screen.getByText('Delete "Due Spanish"? This can\'t be undone.')).toBeInTheDocument()
  })
})
