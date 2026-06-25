import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// Mock the generated scheduler hook the same way DecksScreen.test.tsx mocks generated
// hooks: at the module path the hook under test imports it from, returning the
// React-Query-shaped `{ data, isPending }` directly rather than hitting the network.
vi.mock('../../lib/api/scheduler/study-service/study-service', () => ({
  useStudyServiceGetQueue: vi.fn(),
}))

async function renderUseStudyQueue(deckId: string) {
  const { useStudyQueue } = await import('./useStudyQueue.ts')
  return renderHook(({ id }: { id: string }) => useStudyQueue(id), {
    initialProps: { id: deckId },
  })
}

describe('useStudyQueue', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.resetModules()
  })

  it('exposes the LOADING state while the queue fetch is pending', async () => {
    const { useStudyServiceGetQueue } =
      await import('../../lib/api/scheduler/study-service/study-service')
    vi.mocked(useStudyServiceGetQueue).mockReturnValue({
      data: undefined,
      isPending: true,
    } as never)

    const { result } = await renderUseStudyQueue('deck-1')

    expect(result.current.state).toEqual({ status: 'loading' })
    expect(result.current.currentCard).toBeNull()
  })

  it('initializes the machine to empty when the queue has no cards', async () => {
    const { useStudyServiceGetQueue } =
      await import('../../lib/api/scheduler/study-service/study-service')
    vi.mocked(useStudyServiceGetQueue).mockReturnValue({
      data: { data: { cards: [] } },
      isPending: false,
    } as never)

    const { result } = await renderUseStudyQueue('deck-1')

    expect(result.current.state).toEqual({ status: 'empty' })
    expect(result.current.currentCard).toBeNull()
  })

  it('initializes showingFront with the mapped first card, filtering entries missing cardId/frontHtml', async () => {
    const { useStudyServiceGetQueue } =
      await import('../../lib/api/scheduler/study-service/study-service')
    vi.mocked(useStudyServiceGetQueue).mockReturnValue({
      data: {
        data: {
          cards: [
            { cardId: 'card-1', deckId: 'deck-1', frontHtml: '<p>front-1</p>' },
            { deckId: 'deck-1', frontHtml: '<p>missing id</p>' },
            { cardId: 'card-3', deckId: 'deck-1' },
            { cardId: 'card-4', deckId: 'deck-1', frontHtml: '<p>front-4</p>' },
          ],
        },
      },
      isPending: false,
    } as never)

    const { result } = await renderUseStudyQueue('deck-1')

    expect(result.current.state).toEqual({
      status: 'showingFront',
      queue: [
        { cardId: 'card-1', deckId: 'deck-1', frontHtml: '<p>front-1</p>' },
        { cardId: 'card-4', deckId: 'deck-1', frontHtml: '<p>front-4</p>' },
      ],
      cursor: 0,
    })
    expect(result.current.currentCard).toEqual({
      cardId: 'card-1',
      deckId: 'deck-1',
      frontHtml: '<p>front-1</p>',
    })
  })

  it('reveal() transitions showingFront -> showingBack and updates currentCard derivation', async () => {
    const { useStudyServiceGetQueue } =
      await import('../../lib/api/scheduler/study-service/study-service')
    vi.mocked(useStudyServiceGetQueue).mockReturnValue({
      data: { data: { cards: [{ cardId: 'card-1', deckId: 'deck-1', frontHtml: '<p>f</p>' }] } },
      isPending: false,
    } as never)

    const { result } = await renderUseStudyQueue('deck-1')

    act(() => {
      result.current.reveal()
    })

    expect(result.current.state.status).toBe('showingBack')
    expect(result.current.currentCard).toEqual({
      cardId: 'card-1',
      deckId: 'deck-1',
      frontHtml: '<p>f</p>',
    })
  })

  it('advanceAfterGrade() moves to the next card, then to done after the last card', async () => {
    const { useStudyServiceGetQueue } =
      await import('../../lib/api/scheduler/study-service/study-service')
    vi.mocked(useStudyServiceGetQueue).mockReturnValue({
      data: {
        data: {
          cards: [
            { cardId: 'card-1', deckId: 'deck-1', frontHtml: '<p>1</p>' },
            { cardId: 'card-2', deckId: 'deck-1', frontHtml: '<p>2</p>' },
          ],
        },
      },
      isPending: false,
    } as never)

    const { result } = await renderUseStudyQueue('deck-1')

    act(() => {
      result.current.reveal()
    })
    act(() => {
      result.current.advanceAfterGrade()
    })

    expect(result.current.state.status).toBe('showingFront')
    expect(result.current.currentCard?.cardId).toBe('card-2')

    act(() => {
      result.current.reveal()
    })
    act(() => {
      result.current.advanceAfterGrade()
    })

    expect(result.current.state).toEqual({ status: 'done' })
    expect(result.current.currentCard).toBeNull()
  })

  it('undoLastGrade() rewinds from showingFront back to the previous card showingBack', async () => {
    const { useStudyServiceGetQueue } =
      await import('../../lib/api/scheduler/study-service/study-service')
    vi.mocked(useStudyServiceGetQueue).mockReturnValue({
      data: {
        data: {
          cards: [
            { cardId: 'card-1', deckId: 'deck-1', frontHtml: '<p>1</p>' },
            { cardId: 'card-2', deckId: 'deck-1', frontHtml: '<p>2</p>' },
          ],
        },
      },
      isPending: false,
    } as never)

    const { result } = await renderUseStudyQueue('deck-1')

    act(() => {
      result.current.reveal()
    })
    act(() => {
      result.current.advanceAfterGrade()
    })
    expect(result.current.currentCard?.cardId).toBe('card-2')

    act(() => {
      result.current.undoLastGrade()
    })

    expect(result.current.state.status).toBe('showingBack')
    expect(result.current.currentCard?.cardId).toBe('card-1')
  })

  it('does not re-initialize the machine on an unrelated re-render with the same deckId', async () => {
    const { useStudyServiceGetQueue } =
      await import('../../lib/api/scheduler/study-service/study-service')
    vi.mocked(useStudyServiceGetQueue).mockReturnValue({
      data: { data: { cards: [{ cardId: 'card-1', deckId: 'deck-1', frontHtml: '<p>1</p>' }] } },
      isPending: false,
    } as never)

    const { result, rerender } = await renderUseStudyQueue('deck-1')

    act(() => {
      result.current.reveal()
    })
    expect(result.current.state.status).toBe('showingBack')

    rerender({ id: 'deck-1' })

    // Re-rendering with the same deckId (e.g. a parent re-render, or a React Query
    // background refetch resolving with the same data) must not reset progress back
    // to showingFront/loading.
    expect(result.current.state.status).toBe('showingBack')
  })
})
