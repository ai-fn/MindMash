import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import '../../lib/i18n.ts'
import { useStudyPrefsStore } from './studyPrefsStore.ts'

// Mock the generated hooks StudyScreen/useStudyQueue consume, following the same
// per-module vi.mock pattern DecksScreen.test.tsx uses for generated hooks.
vi.mock('../../lib/api/scheduler/study-service/study-service', () => ({
  useStudyServiceGetQueue: vi.fn(),
  useStudyServiceGetPreview: vi.fn(),
  useStudyServiceAnswer: vi.fn(),
  getStudyServiceGetCountsQueryKey: (deckId: string) => ['study-service', 'getCounts', deckId],
}))
vi.mock('../../lib/api/collection/card-service/card-service', () => ({
  useCardServiceGetCard: vi.fn(),
}))
vi.mock('../../lib/api/collection/deck-service/deck-service', () => ({
  useDeckServiceGetDeck: vi.fn(),
}))

async function renderScreen(initialPath = '/study/deck-1') {
  const { StudyScreen } = await import('./StudyScreen.tsx')
  // Dynamically imported from the same (post vi.resetModules()) module registry as
  // StudyScreen.tsx, so they share the same toastStore singleton — a statically
  // imported Toaster at this file's top would instead resolve against the module
  // graph from this test file's *first* load, which after `vi.resetModules()` in
  // `afterEach` is a different toastStore instance than the one StudyScreen.tsx's
  // fresh dynamic import gets, so its toast() calls would never reach it.
  const { Toaster } = await import('../../ui')
  const queryClient = new QueryClient()
  const result = render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[initialPath]}>
        <Routes>
          <Route path="/study/:deckId" element={<StudyScreen />} />
          <Route path="/" element={<p>Your decks</p>} />
        </Routes>
      </MemoryRouter>
      <Toaster />
    </QueryClientProvider>,
  )
  return { ...result, queryClient }
}

const oneCardQueue = {
  data: { data: { cards: [{ cardId: 'card-1', deckId: 'deck-1', frontHtml: '<p>front-1</p>' }] } },
  isPending: false,
}

const twoCardQueue = {
  data: {
    data: {
      cards: [
        { cardId: 'card-1', deckId: 'deck-1', frontHtml: '<p>front-1</p>' },
        { cardId: 'card-2', deckId: 'deck-1', frontHtml: '<p>front-2</p>' },
      ],
    },
  },
  isPending: false,
}

describe('StudyScreen', () => {
  let mocks: {
    useStudyServiceGetQueue: typeof vi.fn
    useStudyServiceGetPreview: typeof vi.fn
    useStudyServiceAnswer: typeof vi.fn
    useCardServiceGetCard: typeof vi.fn
    useDeckServiceGetDeck: typeof vi.fn
  }

  beforeEach(async () => {
    vi.clearAllMocks()
    window.localStorage.clear()
    useStudyPrefsStore.getState().setAdvancedGrading(false)

    const studyService = await import('../../lib/api/scheduler/study-service/study-service')
    const cardService = await import('../../lib/api/collection/card-service/card-service')
    const deckService = await import('../../lib/api/collection/deck-service/deck-service')
    mocks = {
      useStudyServiceGetQueue: studyService.useStudyServiceGetQueue,
      useStudyServiceGetPreview: studyService.useStudyServiceGetPreview,
      useStudyServiceAnswer: studyService.useStudyServiceAnswer,
      useCardServiceGetCard: cardService.useCardServiceGetCard,
      useDeckServiceGetDeck: deckService.useDeckServiceGetDeck,
    } as never

    vi.mocked(mocks.useCardServiceGetCard).mockReturnValue({
      data: { data: { backHtml: '<p>back-1</p>' } },
      isPending: false,
    } as never)
    vi.mocked(mocks.useStudyServiceGetPreview).mockReturnValue({
      data: {
        data: { preview: { againSec: '60', hardSec: '600', goodSec: '86400', easySec: '518400' } },
      },
      isPending: false,
    } as never)
    vi.mocked(mocks.useDeckServiceGetDeck).mockReturnValue({
      data: { data: { deck: { id: 'deck-1', name: 'Spanish Verbs' } } },
      isPending: false,
    } as never)
  })

  afterEach(() => {
    vi.resetModules()
  })

  it('renders the loading message while the queue fetch is pending', async () => {
    vi.mocked(mocks.useStudyServiceGetQueue).mockReturnValue({
      data: undefined,
      isPending: true,
    } as never)

    await renderScreen()

    expect(screen.getByText('Loading cards…')).toBeInTheDocument()
  })

  it('renders the empty-queue message with a link back to decks', async () => {
    vi.mocked(mocks.useStudyServiceGetQueue).mockReturnValue({
      data: { data: { cards: [] } },
      isPending: false,
    } as never)

    await renderScreen()

    expect(screen.getByText('Nothing due right now.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Back to decks' })).toHaveAttribute('href', '/')
  })

  it('shows the deck name and progress in the header', async () => {
    vi.mocked(mocks.useStudyServiceGetQueue).mockReturnValue(twoCardQueue as never)

    await renderScreen()

    expect(screen.getByText('Spanish Verbs')).toBeInTheDocument()
    expect(screen.getByText('1 / 2')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Exit study session' })).toBeInTheDocument()
  })

  it('navigates to / when the exit button is clicked', async () => {
    vi.mocked(mocks.useStudyServiceGetQueue).mockReturnValue(oneCardQueue as never)
    const user = userEvent.setup()

    await renderScreen()
    await user.click(screen.getByRole('button', { name: 'Exit study session' }))

    expect(screen.getByText('Your decks')).toBeInTheDocument()
  })

  it('navigates to / on Escape', async () => {
    vi.mocked(mocks.useStudyServiceGetQueue).mockReturnValue(oneCardQueue as never)

    await renderScreen()
    fireEvent.keyDown(window, { key: 'Escape' })

    expect(screen.getByText('Your decks')).toBeInTheDocument()
  })

  it('shows the front card and a Show answer button, hiding grade buttons', async () => {
    vi.mocked(mocks.useStudyServiceGetQueue).mockReturnValue(oneCardQueue as never)

    await renderScreen()

    expect(screen.getByRole('button', { name: 'Show answer' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: "Don't Know" })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Know' })).not.toBeInTheDocument()
  })

  it("reveals the back of the card and shows the 2 default grade buttons (Don't Know / Know)", async () => {
    vi.mocked(mocks.useStudyServiceGetQueue).mockReturnValue(oneCardQueue as never)
    const user = userEvent.setup()

    await renderScreen()
    await user.click(screen.getByRole('button', { name: 'Show answer' }))

    expect(screen.getByRole('button', { name: "Don't Know" })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Know' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Again/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Easy/ })).not.toBeInTheDocument()
  })

  it('shows all 4 grade buttons with preview labels when advancedGrading is enabled', async () => {
    useStudyPrefsStore.getState().setAdvancedGrading(true)
    vi.mocked(mocks.useStudyServiceGetQueue).mockReturnValue(oneCardQueue as never)
    const user = userEvent.setup()

    await renderScreen()
    await user.click(screen.getByRole('button', { name: 'Show answer' }))

    expect(screen.getByRole('button', { name: /Again/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Hard/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Good/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Easy/ })).toBeInTheDocument()
    expect(screen.getByText('1m')).toBeInTheDocument() // 60s -> "1m"
  })

  it('Space reveals the card while showingFront', async () => {
    vi.mocked(mocks.useStudyServiceGetQueue).mockReturnValue(oneCardQueue as never)

    await renderScreen()
    fireEvent.keyDown(window, { code: 'Space', key: ' ' })

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Know' })).toBeInTheDocument()
    })
  })

  it('grades via clicking Know, calls the answer mutation with GRADE_GOOD, and advances to the next card on success', async () => {
    vi.mocked(mocks.useStudyServiceGetQueue).mockReturnValue(twoCardQueue as never)
    const mutate = vi.fn((_vars, opts: { onSuccess?: () => void }) => {
      opts.onSuccess?.()
    })
    vi.mocked(mocks.useStudyServiceAnswer).mockReturnValue({ mutate } as never)
    const user = userEvent.setup()

    const { queryClient } = await renderScreen()
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
    await user.click(screen.getByRole('button', { name: 'Show answer' }))
    await user.click(screen.getByRole('button', { name: 'Know' }))

    expect(mutate).toHaveBeenCalledWith(
      { cardId: 'card-1', data: { grade: 'GRADE_GOOD' } },
      expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
    )
    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Show answer' })).toBeInTheDocument()
    })
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: ['study-service', 'getCounts', 'deck-1'],
    })
  })

  it("grades via clicking Don't Know, calls the answer mutation with GRADE_AGAIN", async () => {
    vi.mocked(mocks.useStudyServiceGetQueue).mockReturnValue(oneCardQueue as never)
    const mutate = vi.fn((_vars, opts: { onSuccess?: () => void }) => {
      opts.onSuccess?.()
    })
    vi.mocked(mocks.useStudyServiceAnswer).mockReturnValue({ mutate } as never)
    const user = userEvent.setup()

    await renderScreen()
    await user.click(screen.getByRole('button', { name: 'Show answer' }))
    await user.click(screen.getByRole('button', { name: "Don't Know" }))

    expect(mutate).toHaveBeenCalledWith(
      { cardId: 'card-1', data: { grade: 'GRADE_AGAIN' } },
      expect.anything(),
    )
  })

  it('grades via the 1/2 keyboard shortcuts while showingBack in default mode', async () => {
    vi.mocked(mocks.useStudyServiceGetQueue).mockReturnValue(oneCardQueue as never)
    const mutate = vi.fn((_vars, opts: { onSuccess?: () => void }) => {
      opts.onSuccess?.()
    })
    vi.mocked(mocks.useStudyServiceAnswer).mockReturnValue({ mutate } as never)
    const user = userEvent.setup()

    await renderScreen()
    await user.click(screen.getByRole('button', { name: 'Show answer' }))
    fireEvent.keyDown(window, { key: '2' })

    expect(mutate).toHaveBeenCalledWith(
      { cardId: 'card-1', data: { grade: 'GRADE_GOOD' } },
      expect.anything(),
    )
  })

  it('grades via the 1-4 keyboard shortcuts while showingBack in advanced mode', async () => {
    useStudyPrefsStore.getState().setAdvancedGrading(true)
    vi.mocked(mocks.useStudyServiceGetQueue).mockReturnValue(oneCardQueue as never)
    const mutate = vi.fn((_vars, opts: { onSuccess?: () => void }) => {
      opts.onSuccess?.()
    })
    vi.mocked(mocks.useStudyServiceAnswer).mockReturnValue({ mutate } as never)
    const user = userEvent.setup()

    await renderScreen()
    await user.click(screen.getByRole('button', { name: 'Show answer' }))
    fireEvent.keyDown(window, { key: '3' })

    expect(mutate).toHaveBeenCalledWith(
      { cardId: 'card-1', data: { grade: 'GRADE_GOOD' } },
      expect.anything(),
    )
  })

  it('shows the deck-complete message with a link back to decks after grading the last card', async () => {
    vi.mocked(mocks.useStudyServiceGetQueue).mockReturnValue(oneCardQueue as never)
    const mutate = vi.fn((_vars, opts: { onSuccess?: () => void }) => {
      opts.onSuccess?.()
    })
    vi.mocked(mocks.useStudyServiceAnswer).mockReturnValue({ mutate } as never)
    const user = userEvent.setup()

    await renderScreen()
    await user.click(screen.getByRole('button', { name: 'Show answer' }))
    await user.click(screen.getByRole('button', { name: 'Know' }))

    expect(screen.getByText('Deck complete — nice work!')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Back to decks' })).toHaveAttribute('href', '/')
  })

  it('disables grade buttons while the answer mutation is in-flight and ignores a racing keyboard grade (no double-submit)', async () => {
    vi.mocked(mocks.useStudyServiceGetQueue).mockReturnValue(oneCardQueue as never)
    const mutate = vi.fn() // never resolves -> stays "in flight"
    vi.mocked(mocks.useStudyServiceAnswer).mockReturnValue({ mutate } as never)
    const user = userEvent.setup()

    await renderScreen()
    await user.click(screen.getByRole('button', { name: 'Show answer' }))
    await user.click(screen.getByRole('button', { name: 'Know' }))

    expect(mutate).toHaveBeenCalledTimes(1)
    expect(screen.getByRole('button', { name: 'Know' })).toBeDisabled()

    fireEvent.keyDown(window, { key: '1' })

    expect(mutate).toHaveBeenCalledTimes(1)
  })

  it('shows an inline error and re-enables grade buttons when the answer mutation fails', async () => {
    vi.mocked(mocks.useStudyServiceGetQueue).mockReturnValue(oneCardQueue as never)
    const mutate = vi.fn((_vars, opts: { onError?: () => void }) => {
      opts.onError?.()
    })
    vi.mocked(mocks.useStudyServiceAnswer).mockReturnValue({ mutate } as never)
    const user = userEvent.setup()

    await renderScreen()
    await user.click(screen.getByRole('button', { name: 'Show answer' }))
    await user.click(screen.getByRole('button', { name: 'Know' }))

    expect(screen.getByText("Couldn't submit that grade. Try again.")).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Know' })).not.toBeDisabled()
  })

  it('shows an Undo toast after a successful grade, and undo rewinds to the just-graded card', async () => {
    vi.mocked(mocks.useStudyServiceGetQueue).mockReturnValue(twoCardQueue as never)
    const mutate = vi.fn((_vars, opts: { onSuccess?: () => void }) => {
      opts.onSuccess?.()
    })
    vi.mocked(mocks.useStudyServiceAnswer).mockReturnValue({ mutate } as never)
    const user = userEvent.setup()

    await renderScreen()
    await user.click(screen.getByRole('button', { name: 'Show answer' }))
    await user.click(screen.getByRole('button', { name: 'Know' }))

    // Now on card 2's front.
    await waitFor(() => {
      expect(screen.getByText('2 / 2')).toBeInTheDocument()
    })

    const undoButton = await screen.findByRole('button', { name: 'Undo' })
    await user.click(undoButton)

    // Rewound to card 1's back (showingBack), so its grade buttons are visible again.
    await waitFor(() => {
      expect(screen.getByText('1 / 2')).toBeInTheDocument()
    })
    expect(screen.getByRole('button', { name: 'Know' })).toBeInTheDocument()
  })
})
