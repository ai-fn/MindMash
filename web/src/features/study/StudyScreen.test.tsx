import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import '../../lib/i18n.ts'

// Mock the four generated hooks StudyScreen/useStudyQueue consume, following the same
// per-module vi.mock pattern DecksScreen.test.tsx uses for generated hooks.
vi.mock('../../lib/api/scheduler/study-service/study-service', () => ({
  useStudyServiceGetQueue: vi.fn(),
  useStudyServiceGetPreview: vi.fn(),
  useStudyServiceAnswer: vi.fn(),
}))
vi.mock('../../lib/api/collection/card-service/card-service', () => ({
  useCardServiceGetCard: vi.fn(),
}))

async function renderScreen(initialPath = '/study/deck-1') {
  const { StudyScreen } = await import('./StudyScreen.tsx')
  const queryClient = new QueryClient()
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[initialPath]}>
        <Routes>
          <Route path="/study/:deckId" element={<StudyScreen />} />
          <Route path="/" element={<p>Your decks</p>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
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
  }

  beforeEach(async () => {
    vi.clearAllMocks()
    const studyService = await import('../../lib/api/scheduler/study-service/study-service')
    const cardService = await import('../../lib/api/collection/card-service/card-service')
    mocks = {
      useStudyServiceGetQueue: studyService.useStudyServiceGetQueue,
      useStudyServiceGetPreview: studyService.useStudyServiceGetPreview,
      useStudyServiceAnswer: studyService.useStudyServiceAnswer,
      useCardServiceGetCard: cardService.useCardServiceGetCard,
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

  it('shows the front card and a Show answer button, hiding grade buttons', async () => {
    vi.mocked(mocks.useStudyServiceGetQueue).mockReturnValue(oneCardQueue as never)

    await renderScreen()

    expect(screen.getByRole('button', { name: 'Show answer' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Again' })).not.toBeInTheDocument()
  })

  it('reveals the back of the card and shows grade buttons with preview sub-labels when the Show answer button is clicked', async () => {
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
      expect(screen.getByRole('button', { name: /Again/ })).toBeInTheDocument()
    })
  })

  it('grades via clicking a grade button, calls the answer mutation, and advances to the next card on success', async () => {
    vi.mocked(mocks.useStudyServiceGetQueue).mockReturnValue(twoCardQueue as never)
    const mutate = vi.fn((_vars, opts: { onSuccess?: () => void }) => {
      opts.onSuccess?.()
    })
    vi.mocked(mocks.useStudyServiceAnswer).mockReturnValue({ mutate } as never)
    const user = userEvent.setup()

    await renderScreen()
    await user.click(screen.getByRole('button', { name: 'Show answer' }))
    await user.click(screen.getByRole('button', { name: /Good/ }))

    expect(mutate).toHaveBeenCalledWith(
      { cardId: 'card-1', data: { grade: 'GRADE_GOOD' } },
      expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
    )
    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Show answer' })).toBeInTheDocument()
    })
  })

  it('grades via the 1-4 keyboard shortcuts while showingBack', async () => {
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
    await user.click(screen.getByRole('button', { name: /Good/ }))

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
    await user.click(screen.getByRole('button', { name: /Good/ }))

    expect(mutate).toHaveBeenCalledTimes(1)
    expect(screen.getByRole('button', { name: /Good/ })).toBeDisabled()

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
    await user.click(screen.getByRole('button', { name: /Good/ }))

    expect(screen.getByText("Couldn't submit that grade. Try again.")).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Good/ })).not.toBeDisabled()
  })
})
