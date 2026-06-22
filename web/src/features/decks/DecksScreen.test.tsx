import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import '../../lib/i18n.ts'
import { useAuthStore } from '../auth/authStore'

// Mock the two generated hook modules DecksScreen/DeckRow consume, so this test
// never hits the network and can drive each hook's return shape directly.
vi.mock('../../lib/api/collection/deck-service/deck-service', () => ({
  useDeckServiceListDecks: vi.fn(),
}))
vi.mock('../../lib/api/scheduler/study-service/study-service', () => ({
  useStudyServiceGetCounts: vi.fn(),
}))

async function renderScreen() {
  const { DecksScreen } = await import('./DecksScreen.tsx')
  return render(
    <MemoryRouter>
      <DecksScreen />
    </MemoryRouter>,
  )
}

describe('DecksScreen', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    useAuthStore.setState({ status: 'unauthenticated' })
  })

  it('renders the deck list with names + counts when decks are returned', async () => {
    const { useDeckServiceListDecks } =
      await import('../../lib/api/collection/deck-service/deck-service')
    const { useStudyServiceGetCounts } =
      await import('../../lib/api/scheduler/study-service/study-service')
    vi.mocked(useDeckServiceListDecks).mockReturnValue({
      data: { data: { items: [{ id: 'deck-1', name: 'Spanish' }] } },
      isPending: false,
    } as never)
    vi.mocked(useStudyServiceGetCounts).mockReturnValue({
      data: { data: { newCount: 3, learningCount: 1, reviewCount: 5 } },
      isPending: false,
    } as never)

    await renderScreen()

    expect(screen.getByText('Spanish')).toBeInTheDocument()
    expect(screen.getByText('New: 3')).toBeInTheDocument()
    expect(screen.getByText('Learning: 1')).toBeInTheDocument()
    expect(screen.getByText('Review: 5')).toBeInTheDocument()
  })

  it('renders the empty-state message when items is empty', async () => {
    const { useDeckServiceListDecks } =
      await import('../../lib/api/collection/deck-service/deck-service')
    const { useStudyServiceGetCounts } =
      await import('../../lib/api/scheduler/study-service/study-service')
    vi.mocked(useDeckServiceListDecks).mockReturnValue({
      data: { data: { items: [] } },
      isPending: false,
    } as never)
    vi.mocked(useStudyServiceGetCounts).mockReturnValue({
      data: undefined,
      isPending: true,
    } as never)

    await renderScreen()

    expect(screen.getByText("You don't have any decks yet.")).toBeInTheDocument()
  })

  it('renders the empty-state message when items is undefined', async () => {
    const { useDeckServiceListDecks } =
      await import('../../lib/api/collection/deck-service/deck-service')
    const { useStudyServiceGetCounts } =
      await import('../../lib/api/scheduler/study-service/study-service')
    vi.mocked(useDeckServiceListDecks).mockReturnValue({
      data: { data: {} },
      isPending: false,
    } as never)
    vi.mocked(useStudyServiceGetCounts).mockReturnValue({
      data: undefined,
      isPending: true,
    } as never)

    await renderScreen()

    expect(screen.getByText("You don't have any decks yet.")).toBeInTheDocument()
  })

  it('links each deck row to /study/{deckId}', async () => {
    const { useDeckServiceListDecks } =
      await import('../../lib/api/collection/deck-service/deck-service')
    const { useStudyServiceGetCounts } =
      await import('../../lib/api/scheduler/study-service/study-service')
    vi.mocked(useDeckServiceListDecks).mockReturnValue({
      data: { data: { items: [{ id: 'deck-42', name: 'Japanese' }] } },
      isPending: false,
    } as never)
    vi.mocked(useStudyServiceGetCounts).mockReturnValue({
      data: { data: { newCount: 0, learningCount: 0, reviewCount: 0 } },
      isPending: false,
    } as never)

    await renderScreen()

    expect(screen.getByRole('link')).toHaveAttribute('href', '/study/deck-42')
  })

  it('calls logout when the Logout button is clicked', async () => {
    const { useDeckServiceListDecks } =
      await import('../../lib/api/collection/deck-service/deck-service')
    const { useStudyServiceGetCounts } =
      await import('../../lib/api/scheduler/study-service/study-service')
    vi.mocked(useDeckServiceListDecks).mockReturnValue({
      data: { data: { items: [] } },
      isPending: false,
    } as never)
    vi.mocked(useStudyServiceGetCounts).mockReturnValue({
      data: undefined,
      isPending: true,
    } as never)
    const logoutSpy = vi.fn().mockResolvedValue(undefined)
    useAuthStore.setState({ logout: logoutSpy })

    const user = userEvent.setup()
    await renderScreen()
    await user.click(screen.getByRole('button', { name: 'Log out' }))

    expect(logoutSpy).toHaveBeenCalledTimes(1)
  })

  it('shows a loading message while decks are pending', async () => {
    const { useDeckServiceListDecks } =
      await import('../../lib/api/collection/deck-service/deck-service')
    const { useStudyServiceGetCounts } =
      await import('../../lib/api/scheduler/study-service/study-service')
    vi.mocked(useDeckServiceListDecks).mockReturnValue({
      data: undefined,
      isPending: true,
    } as never)
    vi.mocked(useStudyServiceGetCounts).mockReturnValue({
      data: undefined,
      isPending: true,
    } as never)

    await renderScreen()

    expect(screen.getByText('Loading decks…')).toBeInTheDocument()
  })
})
