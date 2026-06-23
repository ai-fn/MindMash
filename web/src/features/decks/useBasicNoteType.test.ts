import { renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// Mock the generated note-type-service hooks the same way DecksScreen.test.tsx mocks
// generated hooks: at the module path the hook under test imports them from,
// returning the React-Query-shaped `{ data, isPending }` / mutation object directly
// rather than hitting the network.
vi.mock('../../lib/api/collection/note-type-service/note-type-service', () => ({
  useNoteTypeServiceListNoteTypes: vi.fn(),
  useNoteTypeServiceCreateNoteType: vi.fn(),
}))

async function renderUseBasicNoteType() {
  const { useBasicNoteType } = await import('./useBasicNoteType.ts')
  return renderHook(() => useBasicNoteType())
}

const BASIC_NOTE_TYPE_PAYLOAD = {
  name: 'Basic',
  fields: [{ name: 'Front' }, { name: 'Back' }],
  cardTemplates: [
    {
      name: 'Card 1',
      front: '{{Front}}',
      back: '{{FrontSide}}<hr>{{Back}}',
    },
  ],
}

describe('useBasicNoteType', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.resetModules()
  })

  it('resolves the existing "Basic" note type id without calling create', async () => {
    const { useNoteTypeServiceListNoteTypes, useNoteTypeServiceCreateNoteType } =
      await import('../../lib/api/collection/note-type-service/note-type-service')
    vi.mocked(useNoteTypeServiceListNoteTypes).mockReturnValue({
      data: { data: { items: [{ id: 'nt-1', name: 'Basic' }] } },
      isPending: false,
    } as never)
    const mutate = vi.fn()
    vi.mocked(useNoteTypeServiceCreateNoteType).mockReturnValue({
      mutate,
      data: undefined,
    } as never)

    const { result } = await renderUseBasicNoteType()

    expect(result.current.noteTypeId).toBe('nt-1')
    expect(result.current.isPending).toBe(false)
    expect(mutate).not.toHaveBeenCalled()
  })

  it('creates the "Basic" note type when none exists, resolving noteTypeId once the mutation succeeds', async () => {
    const { useNoteTypeServiceListNoteTypes, useNoteTypeServiceCreateNoteType } =
      await import('../../lib/api/collection/note-type-service/note-type-service')
    vi.mocked(useNoteTypeServiceListNoteTypes).mockReturnValue({
      data: { data: { items: [] } },
      isPending: false,
    } as never)
    const mutate = vi.fn()
    vi.mocked(useNoteTypeServiceCreateNoteType).mockReturnValue({
      mutate,
      data: undefined,
    } as never)

    const { result, rerender } = await renderUseBasicNoteType()

    expect(mutate).toHaveBeenCalledTimes(1)
    expect(mutate).toHaveBeenCalledWith({ data: BASIC_NOTE_TYPE_PAYLOAD })
    expect(result.current.noteTypeId).toBeNull()
    expect(result.current.isPending).toBe(true)

    // Simulate the mutation resolving: the create hook now reports the created
    // note type's data.
    vi.mocked(useNoteTypeServiceCreateNoteType).mockReturnValue({
      mutate,
      data: { data: { noteType: { id: 'nt-new', name: 'Basic' } } },
    } as never)
    rerender()

    expect(result.current.noteTypeId).toBe('nt-new')
    expect(result.current.isPending).toBe(false)
  })

  it('never fires a second create call across re-renders (create-once guard)', async () => {
    const { useNoteTypeServiceListNoteTypes, useNoteTypeServiceCreateNoteType } =
      await import('../../lib/api/collection/note-type-service/note-type-service')
    vi.mocked(useNoteTypeServiceListNoteTypes).mockReturnValue({
      data: { data: { items: [] } },
      isPending: false,
    } as never)
    const mutate = vi.fn()
    vi.mocked(useNoteTypeServiceCreateNoteType).mockReturnValue({
      mutate,
      data: undefined,
    } as never)

    const { rerender } = await renderUseBasicNoteType()
    expect(mutate).toHaveBeenCalledTimes(1)

    rerender()
    rerender()

    expect(mutate).toHaveBeenCalledTimes(1)
  })
})
