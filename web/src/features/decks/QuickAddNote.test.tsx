import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import '../../lib/i18n.ts'

// Mock the generated note-type-service + note-service hooks the same way
// DecksScreen.test.tsx mocks generated hooks, at the module paths
// useBasicNoteType/QuickAddNote import them from.
vi.mock('../../lib/api/collection/note-type-service/note-type-service', () => ({
  useNoteTypeServiceListNoteTypes: vi.fn(),
  useNoteTypeServiceCreateNoteType: vi.fn(),
}))
vi.mock('../../lib/api/collection/note-service/note-service', () => ({
  useNoteServiceCreateNote: vi.fn(),
}))
vi.mock('../../lib/api/scheduler/study-service/study-service', () => ({
  getStudyServiceGetCountsQueryKey: (deckId: string) => ['study-service', 'getCounts', deckId],
}))

async function renderQuickAddNote(deckId = 'deck-1', onAdded?: () => void) {
  const { QuickAddNote } = await import('./QuickAddNote.tsx')
  const queryClient = new QueryClient()
  const result = render(
    <QueryClientProvider client={queryClient}>
      <QuickAddNote deckId={deckId} onAdded={onAdded} />
    </QueryClientProvider>,
  )
  return { ...result, queryClient }
}

function mockResolvedBasicNoteType() {
  return Promise.all([import('../../lib/api/collection/note-type-service/note-type-service')]).then(
    ([noteTypeService]) => {
      vi.mocked(noteTypeService.useNoteTypeServiceListNoteTypes).mockReturnValue({
        data: { data: { items: [{ id: 'nt-1', name: 'Basic' }] } },
        isPending: false,
      } as never)
      vi.mocked(noteTypeService.useNoteTypeServiceCreateNoteType).mockReturnValue({
        mutate: vi.fn(),
        data: undefined,
      } as never)
    },
  )
}

describe('QuickAddNote', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.resetModules()
  })

  it('shows a preparing state and disables the form while the note type is unresolved', async () => {
    const { useNoteTypeServiceListNoteTypes, useNoteTypeServiceCreateNoteType } =
      await import('../../lib/api/collection/note-type-service/note-type-service')
    vi.mocked(useNoteTypeServiceListNoteTypes).mockReturnValue({
      data: undefined,
      isPending: true,
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

    await renderQuickAddNote()

    expect(screen.getByText('Preparing…')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Add note' })).toBeDisabled()
  })

  it('disables submit while Front/Back are empty even once the note type resolves', async () => {
    await mockResolvedBasicNoteType()
    const { useNoteServiceCreateNote } =
      await import('../../lib/api/collection/note-service/note-service')
    vi.mocked(useNoteServiceCreateNote).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
    } as never)

    await renderQuickAddNote()

    expect(screen.getByRole('button', { name: 'Add note' })).toBeDisabled()
  })

  it('enables submit once Front+Back are filled, calls the mutation with the right payload, and clears inputs on success', async () => {
    await mockResolvedBasicNoteType()
    const mutate = vi.fn((_vars, opts: { onSuccess?: () => void }) => {
      opts.onSuccess?.()
    })
    const { useNoteServiceCreateNote } =
      await import('../../lib/api/collection/note-service/note-service')
    vi.mocked(useNoteServiceCreateNote).mockReturnValue({ mutate, isPending: false } as never)
    const user = userEvent.setup()

    const { queryClient } = await renderQuickAddNote('deck-42')
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
    const frontInput = screen.getByLabelText('Front')
    const backInput = screen.getByLabelText('Back')
    await user.type(frontInput, 'Hola')
    await user.type(backInput, 'Hello')

    expect(screen.getByRole('button', { name: 'Add note' })).not.toBeDisabled()

    await user.click(screen.getByRole('button', { name: 'Add note' }))

    expect(mutate).toHaveBeenCalledWith(
      {
        data: {
          noteTypeId: 'nt-1',
          deckId: 'deck-42',
          fields: { Front: 'Hola', Back: 'Hello' },
        },
      },
      expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
    )
    expect(frontInput).toHaveValue('')
    expect(backInput).toHaveValue('')
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: ['study-service', 'getCounts', 'deck-42'],
    })
  })

  it('calls onAdded after a successful add so the parent can collapse the form', async () => {
    await mockResolvedBasicNoteType()
    const mutate = vi.fn((_vars, opts: { onSuccess?: () => void }) => {
      opts.onSuccess?.()
    })
    const { useNoteServiceCreateNote } =
      await import('../../lib/api/collection/note-service/note-service')
    vi.mocked(useNoteServiceCreateNote).mockReturnValue({ mutate, isPending: false } as never)
    const onAdded = vi.fn()
    const user = userEvent.setup()

    await renderQuickAddNote('deck-42', onAdded)
    await user.type(screen.getByLabelText('Front'), 'Hola')
    await user.type(screen.getByLabelText('Back'), 'Hello')
    await user.click(screen.getByRole('button', { name: 'Add note' }))

    expect(onAdded).toHaveBeenCalledTimes(1)
  })

  it('disables submit while the create-note mutation is in-flight', async () => {
    await mockResolvedBasicNoteType()
    const { useNoteServiceCreateNote } =
      await import('../../lib/api/collection/note-service/note-service')
    vi.mocked(useNoteServiceCreateNote).mockReturnValue({
      mutate: vi.fn(),
      isPending: true,
    } as never)
    const user = userEvent.setup()

    await renderQuickAddNote()
    await user.type(screen.getByLabelText('Front'), 'Hola')
    await user.type(screen.getByLabelText('Back'), 'Hello')

    expect(screen.getByRole('button', { name: 'Add note' })).toBeDisabled()
  })

  it('shows an inline error on failure', async () => {
    await mockResolvedBasicNoteType()
    const mutate = vi.fn((_vars, opts: { onError?: () => void }) => {
      opts.onError?.()
    })
    const { useNoteServiceCreateNote } =
      await import('../../lib/api/collection/note-service/note-service')
    vi.mocked(useNoteServiceCreateNote).mockReturnValue({ mutate, isPending: false } as never)
    const user = userEvent.setup()

    await renderQuickAddNote()
    await user.type(screen.getByLabelText('Front'), 'Hola')
    await user.type(screen.getByLabelText('Back'), 'Hello')
    await user.click(screen.getByRole('button', { name: 'Add note' }))

    expect(screen.getByText('Something went wrong. Try again.')).toBeInTheDocument()
  })
})
