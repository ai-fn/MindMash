import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import '../../lib/i18n.ts'

// Mock the generated hooks CardDetail consumes, following the same per-module vi.mock
// pattern BrowseScreen.test.tsx / NewDeck.test.tsx use for generated hooks.
vi.mock('../../lib/api/collection/card-service/card-service', () => ({
  useCardServiceGetCard: vi.fn(),
}))
vi.mock('../../lib/api/collection/note-service/note-service', () => ({
  useNoteServiceGetNote: vi.fn(),
  useNoteServiceUpdateNote: vi.fn(),
  useNoteServiceDeleteNote: vi.fn(),
  getNoteServiceGetNoteQueryKey: vi.fn((id: string) => [`/v1/notes/${id}`]),
}))

const TOAST_MOCK = vi.fn()
vi.mock('../../ui', async () => {
  const actual = await vi.importActual<typeof import('../../ui')>('../../ui')
  return { ...actual, toast: TOAST_MOCK }
})

function makeCard(overrides: Record<string, unknown> = {}) {
  return {
    id: 'card-1',
    noteId: 'note-1',
    deckId: 'deck-1',
    ...overrides,
  }
}

function makeNote(overrides: Record<string, unknown> = {}) {
  return {
    id: 'note-1',
    noteTypeId: 'nt-1',
    fields: { Front: 'manger', Back: 'to eat' },
    tags: ['verb'],
    flags: 0,
    usn: 'usn-7',
    ...overrides,
  }
}

async function setupHooks({
  card = makeCard(),
  note = makeNote(),
  cardPending = false,
  cardError = false,
  notePending = false,
  noteError = false,
  updateMutate = vi.fn(),
  updatePending = false,
  deleteMutate = vi.fn(),
  deletePending = false,
}: {
  card?: ReturnType<typeof makeCard> | undefined
  note?: ReturnType<typeof makeNote> | undefined
  cardPending?: boolean
  cardError?: boolean
  notePending?: boolean
  noteError?: boolean
  updateMutate?: ReturnType<typeof vi.fn>
  updatePending?: boolean
  deleteMutate?: ReturnType<typeof vi.fn>
  deletePending?: boolean
} = {}) {
  const { useCardServiceGetCard } =
    await import('../../lib/api/collection/card-service/card-service')
  const { useNoteServiceGetNote, useNoteServiceUpdateNote, useNoteServiceDeleteNote } =
    await import('../../lib/api/collection/note-service/note-service')

  vi.mocked(useCardServiceGetCard).mockReturnValue({
    data: card
      ? { data: { card, frontHtml: '<p>manger</p>', backHtml: '<p>to eat</p>' } }
      : undefined,
    isPending: cardPending,
    isError: cardError,
  } as never)

  vi.mocked(useNoteServiceGetNote).mockReturnValue({
    data: note ? { data: { note } } : undefined,
    isPending: notePending,
    isError: noteError,
  } as never)

  vi.mocked(useNoteServiceUpdateNote).mockReturnValue({
    mutate: updateMutate,
    isPending: updatePending,
  } as never)

  vi.mocked(useNoteServiceDeleteNote).mockReturnValue({
    mutate: deleteMutate,
    isPending: deletePending,
  } as never)

  return { updateMutate, deleteMutate }
}

async function renderCardDetail(cardId: string | null = 'card-1', onClose = vi.fn()) {
  const { CardDetail } = await import('./CardDetail.tsx')
  const queryClient = new QueryClient()
  const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
  const result = render(
    <QueryClientProvider client={queryClient}>
      <CardDetail cardId={cardId} onClose={onClose} />
    </QueryClientProvider>,
  )
  return { invalidateSpy, container: result.container }
}

describe('CardDetail', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.resetModules()
  })

  it('renders nothing when cardId is null', async () => {
    await setupHooks()
    await renderCardDetail(null)

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('shows a loading state while the card or note query is pending', async () => {
    await setupHooks({ cardPending: true, card: undefined })
    await renderCardDetail()

    expect(document.querySelector('[aria-busy="true"]')).toBeInTheDocument()
  })

  it('loads the card then the note, and renders the preview and a pre-filled form', async () => {
    await setupHooks()
    await renderCardDetail()

    expect(screen.getByDisplayValue('manger')).toBeInTheDocument()
    expect(screen.getByDisplayValue('to eat')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeInTheDocument()
  })

  it('renders a Textarea for long field values and a TextInput for short ones', async () => {
    await setupHooks({
      note: makeNote({ fields: { Front: 'short', Back: 'x'.repeat(120) } }),
    })
    await renderCardDetail()

    const shortField = screen.getByDisplayValue('short')
    const longField = screen.getByDisplayValue('x'.repeat(120))
    expect(shortField.tagName).toBe('INPUT')
    expect(longField.tagName).toBe('TEXTAREA')
  })

  it('submits UpdateNote with edited fields, unchanged tags/flags, and the loaded expectedUsn', async () => {
    const updateMutate = vi.fn((_vars, handlers) => {
      handlers?.onSuccess?.()
    })
    await setupHooks({ updateMutate })
    const user = userEvent.setup()
    const { invalidateSpy } = await renderCardDetail()

    const frontInput = screen.getByDisplayValue('manger')
    await user.clear(frontInput)
    await user.type(frontInput, 'manger (irr.)')

    await user.click(screen.getByRole('button', { name: 'Save changes' }))

    expect(updateMutate).toHaveBeenCalledWith(
      {
        id: 'note-1',
        data: {
          fields: { Front: 'manger (irr.)', Back: 'to eat' },
          tags: ['verb'],
          flags: 0,
          expectedUsn: 'usn-7',
        },
      },
      expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
    )

    await waitFor(() => {
      expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['/v1/notes/note-1'] })
      expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['/v1/search'] })
    })
    expect(TOAST_MOCK).toHaveBeenCalledWith(
      expect.objectContaining({ title: 'Card saved.', tone: 'success' }),
    )
  })

  it('shows an error and does not invalidate or toast when UpdateNote fails', async () => {
    const updateMutate = vi.fn((_vars, handlers) => {
      handlers?.onError?.()
    })
    await setupHooks({ updateMutate })
    const user = userEvent.setup()
    await renderCardDetail()

    await user.click(screen.getByRole('button', { name: 'Save changes' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Something went wrong. Try again.')
    expect(TOAST_MOCK).not.toHaveBeenCalled()
  })

  it('opens a confirm step on "Delete card" without deleting, and Cancel returns to the form', async () => {
    const deleteMutate = vi.fn()
    await setupHooks({ deleteMutate })
    const user = userEvent.setup()
    await renderCardDetail()

    await user.click(screen.getByRole('button', { name: 'Delete card' }))

    expect(
      screen.getByText(
        "Delete this card? This removes the note and all its cards. This can't be undone.",
      ),
    ).toBeInTheDocument()
    expect(deleteMutate).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: 'Cancel' }))

    expect(screen.getByRole('button', { name: 'Save changes' })).toBeInTheDocument()
    expect(deleteMutate).not.toHaveBeenCalled()
  })

  it('calls DeleteNote with expectedUsn only after confirming, then invalidates, closes, and toasts', async () => {
    const onClose = vi.fn()
    const deleteMutate = vi.fn((_vars, handlers) => {
      handlers?.onSuccess?.()
    })
    await setupHooks({ deleteMutate })
    const user = userEvent.setup()
    const { invalidateSpy } = await renderCardDetail('card-1', onClose)

    await user.click(screen.getByRole('button', { name: 'Delete card' }))
    // Two "Delete card" buttons exist once the confirm step is showing: this clicks the
    // confirm-step's action button, which carries the more specific confirm copy.
    await user.click(screen.getByRole('button', { name: 'Delete card' }))

    expect(deleteMutate).toHaveBeenCalledWith(
      { id: 'note-1', params: { expectedUsn: 'usn-7' } },
      expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
    )

    await waitFor(() => {
      expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['/v1/notes/note-1'] })
      expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['/v1/search'] })
    })
    expect(onClose).toHaveBeenCalled()
    expect(TOAST_MOCK).toHaveBeenCalledWith(
      expect.objectContaining({ title: 'Card deleted.', tone: 'success' }),
    )
  })

  it('shows an error and stays open when DeleteNote fails', async () => {
    const onClose = vi.fn()
    const deleteMutate = vi.fn((_vars, handlers) => {
      handlers?.onError?.()
    })
    await setupHooks({ deleteMutate })
    const user = userEvent.setup()
    await renderCardDetail('card-1', onClose)

    await user.click(screen.getByRole('button', { name: 'Delete card' }))
    await user.click(screen.getByRole('button', { name: 'Delete card' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Something went wrong. Try again.')
    expect(onClose).not.toHaveBeenCalled()
    expect(TOAST_MOCK).not.toHaveBeenCalled()
  })
})
