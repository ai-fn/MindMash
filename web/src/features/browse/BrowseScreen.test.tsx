import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import '../../lib/i18n.ts'

// Mock the generated search hook BrowseScreen consumes, following the same per-module
// vi.mock pattern StatsScreen.test.tsx / DecksScreen.test.tsx use for generated hooks.
vi.mock('../../lib/api/search/search-service/search-service', () => ({
  useSearchServiceSearch: vi.fn(),
}))

async function renderScreen(onSelectCard?: (cardId: string) => void) {
  const { BrowseScreen } = await import('./BrowseScreen.tsx')
  const queryClient = new QueryClient()
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <BrowseScreen onSelectCard={onSelectCard} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

function makeCard(overrides: Record<string, unknown> = {}) {
  return {
    cardId: 'card-1',
    noteId: 'note-1',
    deckId: 'deck-1',
    deckPath: 'French::Verbs',
    state: 'review',
    suspended: false,
    ivl: 5,
    reps: 3,
    tags: ['verb', 'irregular'],
    fieldsText: 'manger — to eat',
    ...overrides,
  }
}

describe('BrowseScreen', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.useFakeTimers({ shouldAdvanceTime: true })
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.resetModules()
  })

  it('calls the search hook with an empty q on first render (empty query still searches)', async () => {
    const { useSearchServiceSearch } =
      await import('../../lib/api/search/search-service/search-service')
    vi.mocked(useSearchServiceSearch).mockReturnValue({
      data: { data: { results: [], nextCursor: undefined } },
      isPending: false,
      isFetching: false,
      isError: false,
      refetch: vi.fn(),
    } as never)

    await renderScreen()

    expect(useSearchServiceSearch).toHaveBeenCalledWith({
      q: undefined,
      cursor: undefined,
      limit: 20,
    })
  })

  it('debounces typed input before it reaches the search hook as `q`', async () => {
    const { useSearchServiceSearch } =
      await import('../../lib/api/search/search-service/search-service')
    vi.mocked(useSearchServiceSearch).mockReturnValue({
      data: { data: { results: [], nextCursor: undefined } },
      isPending: false,
      isFetching: false,
      isError: false,
      refetch: vi.fn(),
    } as never)
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })

    await renderScreen()

    await user.type(screen.getByLabelText('Search'), 'deck:French')

    // Immediately after typing (before the debounce window elapses), the hook should not
    // yet have been called with the typed value.
    expect(useSearchServiceSearch).not.toHaveBeenCalledWith(
      expect.objectContaining({ q: 'deck:French' }),
    )

    vi.advanceTimersByTime(300)

    await waitFor(() => {
      expect(useSearchServiceSearch).toHaveBeenLastCalledWith({
        q: 'deck:French',
        cursor: undefined,
        limit: 20,
      })
    })
  })

  it('renders result rows with deck path, state badge, tags, preview, and ivl/reps', async () => {
    const { useSearchServiceSearch } =
      await import('../../lib/api/search/search-service/search-service')
    vi.mocked(useSearchServiceSearch).mockReturnValue({
      data: { data: { results: [makeCard()], nextCursor: undefined } },
      isPending: false,
      isFetching: false,
      isError: false,
      refetch: vi.fn(),
    } as never)

    await renderScreen()

    expect(screen.getByText('French::Verbs')).toBeInTheDocument()
    expect(screen.getByText('Review')).toBeInTheDocument()
    expect(screen.getByText('verb')).toBeInTheDocument()
    expect(screen.getByText('irregular')).toBeInTheDocument()
    expect(screen.getByText('manger — to eat')).toBeInTheDocument()
    expect(screen.getByText('Interval: 5 days')).toBeInTheDocument()
    expect(screen.getByText('Reps: 3')).toBeInTheDocument()
  })

  it('truncates a long fieldsText preview to the character cap', async () => {
    const { useSearchServiceSearch } =
      await import('../../lib/api/search/search-service/search-service')
    const longText = 'x'.repeat(200)
    vi.mocked(useSearchServiceSearch).mockReturnValue({
      data: { data: { results: [makeCard({ fieldsText: longText })], nextCursor: undefined } },
      isPending: false,
      isFetching: false,
      isError: false,
      refetch: vi.fn(),
    } as never)

    await renderScreen()

    const truncated = `${'x'.repeat(140)}…`
    expect(screen.getByText(truncated)).toBeInTheDocument()
    expect(screen.queryByText(longText)).not.toBeInTheDocument()
  })

  it('maps a suspended card to the danger tone regardless of its underlying state', async () => {
    const { useSearchServiceSearch } =
      await import('../../lib/api/search/search-service/search-service')
    vi.mocked(useSearchServiceSearch).mockReturnValue({
      data: {
        data: { results: [makeCard({ state: 'review', suspended: true })], nextCursor: undefined },
      },
      isPending: false,
      isFetching: false,
      isError: false,
      refetch: vi.fn(),
    } as never)

    await renderScreen()

    expect(screen.getByText('Suspended')).toBeInTheDocument()
    expect(screen.queryByText('Review')).not.toBeInTheDocument()
  })

  it('calls onSelectCard with the cardId when a result row is clicked', async () => {
    const { useSearchServiceSearch } =
      await import('../../lib/api/search/search-service/search-service')
    vi.mocked(useSearchServiceSearch).mockReturnValue({
      data: { data: { results: [makeCard({ cardId: 'card-42' })], nextCursor: undefined } },
      isPending: false,
      isFetching: false,
      isError: false,
      refetch: vi.fn(),
    } as never)
    const onSelectCard = vi.fn()
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })

    await renderScreen(onSelectCard)

    await user.click(screen.getByRole('button', { name: 'Card result' }))

    expect(onSelectCard).toHaveBeenCalledWith('card-42')
  })

  it('does not throw when a result row is clicked and no onSelectCard prop is wired in', async () => {
    const { useSearchServiceSearch } =
      await import('../../lib/api/search/search-service/search-service')
    vi.mocked(useSearchServiceSearch).mockReturnValue({
      data: { data: { results: [makeCard()], nextCursor: undefined } },
      isPending: false,
      isFetching: false,
      isError: false,
      refetch: vi.fn(),
    } as never)
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })

    await renderScreen()

    await expect(
      user.click(screen.getByRole('button', { name: 'Card result' })),
    ).resolves.not.toThrow()
  })

  it('shows loading skeletons while the initial query is pending', async () => {
    const { useSearchServiceSearch } =
      await import('../../lib/api/search/search-service/search-service')
    vi.mocked(useSearchServiceSearch).mockReturnValue({
      data: undefined,
      isPending: true,
      isFetching: true,
      isError: false,
      refetch: vi.fn(),
    } as never)

    const { container } = await renderScreen()

    expect(container.querySelector('[aria-busy="true"]')).toBeInTheDocument()
    expect(screen.getByRole('status', { name: 'Loading cards…' })).toBeInTheDocument()
  })

  it('shows the empty state when a query returns zero results', async () => {
    const { useSearchServiceSearch } =
      await import('../../lib/api/search/search-service/search-service')
    vi.mocked(useSearchServiceSearch).mockReturnValue({
      data: { data: { results: [], nextCursor: undefined } },
      isPending: false,
      isFetching: false,
      isError: false,
      refetch: vi.fn(),
    } as never)

    await renderScreen()

    expect(screen.getByText('No cards match.')).toBeInTheDocument()
  })

  it('shows an error state with a retry action when the query fails, and refetch is wired up', async () => {
    const { useSearchServiceSearch } =
      await import('../../lib/api/search/search-service/search-service')
    const refetch = vi.fn()
    vi.mocked(useSearchServiceSearch).mockReturnValue({
      data: undefined,
      isPending: false,
      isFetching: false,
      isError: true,
      refetch,
    } as never)
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })

    await renderScreen()

    expect(screen.getByText("Couldn't load cards.")).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Retry' }))

    expect(refetch).toHaveBeenCalledTimes(1)
  })

  it('shows a "Load more" button when nextCursor is present, hides it otherwise', async () => {
    const { useSearchServiceSearch } =
      await import('../../lib/api/search/search-service/search-service')
    vi.mocked(useSearchServiceSearch).mockReturnValue({
      data: { data: { results: [makeCard()], nextCursor: 'cursor-2' } },
      isPending: false,
      isFetching: false,
      isError: false,
      refetch: vi.fn(),
    } as never)

    await renderScreen()

    expect(screen.getByRole('button', { name: 'Load more' })).toBeInTheDocument()
  })

  it('hides "Load more" when there is no nextCursor', async () => {
    const { useSearchServiceSearch } =
      await import('../../lib/api/search/search-service/search-service')
    vi.mocked(useSearchServiceSearch).mockReturnValue({
      data: { data: { results: [makeCard()], nextCursor: undefined } },
      isPending: false,
      isFetching: false,
      isError: false,
      refetch: vi.fn(),
    } as never)

    await renderScreen()

    expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument()
  })

  it('clicking "Load more" requests the next cursor and appends results without losing the first page', async () => {
    const { useSearchServiceSearch } =
      await import('../../lib/api/search/search-service/search-service')
    const mocked = vi.mocked(useSearchServiceSearch)
    mocked.mockReturnValue({
      data: {
        data: {
          results: [makeCard({ cardId: 'card-1', deckPath: 'French::Verbs' })],
          nextCursor: 'cursor-2',
        },
      },
      isPending: false,
      isFetching: false,
      isError: false,
      refetch: vi.fn(),
    } as never)
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })

    await renderScreen()

    expect(screen.getByText('French::Verbs')).toBeInTheDocument()

    // Simulate the hook returning page 2's data once the cursor param changes.
    mocked.mockReturnValue({
      data: {
        data: {
          results: [makeCard({ cardId: 'card-2', deckPath: 'Spanish::Nouns' })],
          nextCursor: undefined,
        },
      },
      isPending: false,
      isFetching: false,
      isError: false,
      refetch: vi.fn(),
    } as never)

    await user.click(screen.getByRole('button', { name: 'Load more' }))

    expect(useSearchServiceSearch).toHaveBeenLastCalledWith({
      q: undefined,
      cursor: 'cursor-2',
      limit: 20,
    })

    // Both pages' rows remain visible — page 2 appended, page 1 not lost.
    await waitFor(() => {
      expect(screen.getByText('French::Verbs')).toBeInTheDocument()
      expect(screen.getByText('Spanish::Nouns')).toBeInTheDocument()
    })
  })
})
