import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import '../../lib/i18n.ts'
import { useAuthStore } from '../auth/authStore'

// Mock the generated hook modules DecksScreen/DeckRow/NewDeck/QuickAddNote consume,
// so this test never hits the network and can drive each hook's return shape
// directly. NewDeck (Task 7) is rendered unconditionally now, and QuickAddNote
// (also Task 7, behind the per-row "Add card" toggle) pulls in useBasicNoteType's
// note-type-service hooks + the note-service create mutation.
vi.mock('../../lib/api/collection/deck-service/deck-service', () => ({
  useDeckServiceListDecks: vi.fn(),
  useDeckServiceCreateDeck: vi.fn(),
  getDeckServiceListDecksQueryKey: vi.fn(() => ['/v1/decks']),
}))
vi.mock('../../lib/api/scheduler/study-service/study-service', () => ({
  useStudyServiceGetCounts: vi.fn(),
}))
vi.mock('../../lib/api/collection/note-type-service/note-type-service', () => ({
  useNoteTypeServiceListNoteTypes: vi.fn(),
  useNoteTypeServiceCreateNoteType: vi.fn(),
}))
vi.mock('../../lib/api/collection/note-service/note-service', () => ({
  useNoteServiceCreateNote: vi.fn(),
}))

async function renderScreen() {
  const { DecksScreen } = await import('./DecksScreen.tsx')
  const queryClient = new QueryClient()
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <DecksScreen />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('DecksScreen', () => {
  beforeEach(async () => {
    vi.clearAllMocks()
    const { useDeckServiceCreateDeck } =
      await import('../../lib/api/collection/deck-service/deck-service')
    vi.mocked(useDeckServiceCreateDeck).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
    } as never)
    const { useNoteTypeServiceListNoteTypes, useNoteTypeServiceCreateNoteType } =
      await import('../../lib/api/collection/note-type-service/note-type-service')
    vi.mocked(useNoteTypeServiceListNoteTypes).mockReturnValue({
      data: { data: { items: [{ id: 'nt-1', name: 'Basic' }] } },
      isPending: false,
    } as never)
    vi.mocked(useNoteTypeServiceCreateNoteType).mockReturnValue({
      mutate: vi.fn(),
      data: undefined,
    } as never)
    const { useNoteServiceCreateNote } =
      await import('../../lib/api/collection/note-service/note-service')
    vi.mocked(useNoteServiceCreateNote).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
    } as never)
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

  it('renders the NewDeck form even when the deck list is empty', async () => {
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

    expect(screen.getByLabelText('New deck name')).toBeInTheDocument()
    expect(screen.getByText("You don't have any decks yet.")).toBeInTheDocument()
  })

  it('reveals QuickAddNote for a deck row when its "Add card" toggle is clicked, without nesting it inside the deck Link', async () => {
    const { useDeckServiceListDecks } =
      await import('../../lib/api/collection/deck-service/deck-service')
    const { useStudyServiceGetCounts } =
      await import('../../lib/api/scheduler/study-service/study-service')
    vi.mocked(useDeckServiceListDecks).mockReturnValue({
      data: { data: { items: [{ id: 'deck-1', name: 'Spanish' }] } },
      isPending: false,
    } as never)
    vi.mocked(useStudyServiceGetCounts).mockReturnValue({
      data: { data: { newCount: 0, learningCount: 0, reviewCount: 0 } },
      isPending: false,
    } as never)
    const user = userEvent.setup()

    await renderScreen()

    expect(screen.queryByLabelText('Front')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Add card' }))

    const frontInput = screen.getByLabelText('Front')
    expect(frontInput).toBeInTheDocument()
    // The "Add card" button and QuickAddNote's Front input must not be inside the
    // deck-row Link (see task-7-brief.md) — a single link should still resolve to
    // the deck-row link, not wrap the new affordances.
    const link = screen.getByRole('link')
    expect(link).toHaveAttribute('href', '/study/deck-1')
    expect(link).not.toContainElement(frontInput)
    expect(link).not.toContainElement(screen.getByRole('button', { name: 'Add card' }))
  })

  it('end-to-end: a fresh user can create a deck then quick-add a note via that deck row, calling create-deck then create-note (transparently creating the "Basic" note type) with the right payloads', async () => {
    const { useDeckServiceListDecks, useDeckServiceCreateDeck } =
      await import('../../lib/api/collection/deck-service/deck-service')
    const { useStudyServiceGetCounts } =
      await import('../../lib/api/scheduler/study-service/study-service')
    const { useNoteTypeServiceListNoteTypes, useNoteTypeServiceCreateNoteType } =
      await import('../../lib/api/collection/note-type-service/note-type-service')
    const { useNoteServiceCreateNote } =
      await import('../../lib/api/collection/note-service/note-service')

    // Step 1: fresh user — no decks, no note types yet. Create a deck via NewDeck.
    vi.mocked(useDeckServiceListDecks).mockReturnValue({
      data: { data: { items: [] } },
      isPending: false,
    } as never)
    vi.mocked(useStudyServiceGetCounts).mockReturnValue({
      data: undefined,
      isPending: true,
    } as never)
    const createDeckMutate = vi.fn((_vars, opts: { onSuccess?: () => void }) => {
      opts.onSuccess?.()
    })
    vi.mocked(useDeckServiceCreateDeck).mockReturnValue({
      mutate: createDeckMutate,
      isPending: false,
    } as never)
    vi.mocked(useNoteTypeServiceListNoteTypes).mockReturnValue({
      data: { data: { items: [] } },
      isPending: false,
    } as never)
    const createNoteTypeMutate = vi.fn()
    vi.mocked(useNoteTypeServiceCreateNoteType).mockReturnValue({
      mutate: createNoteTypeMutate,
      data: undefined,
    } as never)
    const createNoteMutate = vi.fn()
    vi.mocked(useNoteServiceCreateNote).mockReturnValue({
      mutate: createNoteMutate,
      isPending: false,
    } as never)
    const user = userEvent.setup()

    const { rerender } = await renderScreen()

    await user.type(screen.getByLabelText('New deck name'), 'Spanish')
    await user.click(screen.getByRole('button', { name: 'Create deck' }))

    expect(createDeckMutate).toHaveBeenCalledWith(
      { data: { name: 'Spanish' } },
      expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
    )
    expect(createNoteTypeMutate).not.toHaveBeenCalled()

    // Step 2: simulate the deck list now containing the created deck (a real
    // invalidateQueries-driven refetch would do this) and re-render in place.
    vi.mocked(useDeckServiceListDecks).mockReturnValue({
      data: { data: { items: [{ id: 'deck-new', name: 'Spanish' }] } },
      isPending: false,
    } as never)
    vi.mocked(useStudyServiceGetCounts).mockReturnValue({
      data: { data: { newCount: 0, learningCount: 0, reviewCount: 0 } },
      isPending: false,
    } as never)
    const { DecksScreen } = await import('./DecksScreen.tsx')
    rerender(
      <QueryClientProvider client={new QueryClient()}>
        <MemoryRouter>
          <DecksScreen />
        </MemoryRouter>
      </QueryClientProvider>,
    )

    // Step 3: open "Add card" for the new deck — this mounts QuickAddNote, which
    // transparently triggers useBasicNoteType's create call since no "Basic" note
    // type exists yet.
    await user.click(screen.getByRole('button', { name: 'Add card' }))

    expect(createNoteTypeMutate).toHaveBeenCalledTimes(1)
    expect(createNoteTypeMutate).toHaveBeenCalledWith({
      data: {
        name: 'Basic',
        fields: [{ name: 'Front' }, { name: 'Back' }],
        cardTemplates: [{ name: 'Card 1', front: '{{Front}}', back: '{{FrontSide}}<hr>{{Back}}' }],
      },
    })
    // The note type hasn't resolved yet (mutation hasn't "succeeded" in this mock),
    // so the quick-add form is disabled — submit is gated on noteTypeId.
    expect(screen.getByRole('button', { name: 'Add note' })).toBeDisabled()

    // Step 4: simulate the create-note-type mutation succeeding, then quick-add a
    // note — the note-create call must use the resolved noteTypeId, this deck's id,
    // and the typed Front/Back fields.
    vi.mocked(useNoteTypeServiceCreateNoteType).mockReturnValue({
      mutate: createNoteTypeMutate,
      data: { data: { noteType: { id: 'nt-new', name: 'Basic' } } },
    } as never)
    rerender(
      <QueryClientProvider client={new QueryClient()}>
        <MemoryRouter>
          <DecksScreen />
        </MemoryRouter>
      </QueryClientProvider>,
    )
    // The "Add card" toggle is already open from Step 3 (component state persists
    // across this in-place rerender) — no need to click it again.
    await user.type(screen.getByLabelText('Front'), 'Hola')
    await user.type(screen.getByLabelText('Back'), 'Hello')
    await user.click(screen.getByRole('button', { name: 'Add note' }))

    expect(createNoteMutate).toHaveBeenCalledWith(
      {
        data: {
          noteTypeId: 'nt-new',
          deckId: 'deck-new',
          fields: { Front: 'Hola', Back: 'Hello' },
        },
      },
      expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
    )
  })
})
