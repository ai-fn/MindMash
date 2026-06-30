import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import '../../lib/i18n.ts'

vi.mock('../../lib/api/collection/note-type-service/note-type-service', () => ({
  useNoteTypeServiceListNoteTypes: vi.fn(),
}))

async function renderDialog(open = true, onClose = vi.fn()) {
  const { NoteTypesDialog } = await import('./NoteTypesDialog.tsx')
  render(<NoteTypesDialog open={open} onClose={onClose} />)
}

describe('NoteTypesDialog', () => {
  it('renders nothing when closed', async () => {
    const { useNoteTypeServiceListNoteTypes } =
      await import('../../lib/api/collection/note-type-service/note-type-service')
    vi.mocked(useNoteTypeServiceListNoteTypes).mockReturnValue({
      data: undefined,
      isPending: true,
      isError: false,
    } as never)

    await renderDialog(false)

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('shows a loading state while the list is pending', async () => {
    const { useNoteTypeServiceListNoteTypes } =
      await import('../../lib/api/collection/note-type-service/note-type-service')
    vi.mocked(useNoteTypeServiceListNoteTypes).mockReturnValue({
      data: undefined,
      isPending: true,
      isError: false,
    } as never)

    await renderDialog()

    expect(screen.getByRole('status', { name: 'Loading note types…' })).toBeInTheDocument()
  })

  it('shows an error state when the list fails', async () => {
    const { useNoteTypeServiceListNoteTypes } =
      await import('../../lib/api/collection/note-type-service/note-type-service')
    vi.mocked(useNoteTypeServiceListNoteTypes).mockReturnValue({
      data: undefined,
      isPending: false,
      isError: true,
    } as never)

    await renderDialog()

    expect(screen.getByRole('alert')).toHaveTextContent('Something went wrong. Try again.')
  })

  it('shows an empty state when there are no note types', async () => {
    const { useNoteTypeServiceListNoteTypes } =
      await import('../../lib/api/collection/note-type-service/note-type-service')
    vi.mocked(useNoteTypeServiceListNoteTypes).mockReturnValue({
      data: { data: { items: [] } },
      isPending: false,
      isError: false,
    } as never)

    await renderDialog()

    expect(screen.getByText('No note types yet.')).toBeInTheDocument()
  })

  it('renders each note type with its field and template counts', async () => {
    const { useNoteTypeServiceListNoteTypes } =
      await import('../../lib/api/collection/note-type-service/note-type-service')
    vi.mocked(useNoteTypeServiceListNoteTypes).mockReturnValue({
      data: {
        data: {
          items: [
            {
              id: 'nt-1',
              name: 'Basic',
              fields: [{ name: 'Front' }, { name: 'Back' }],
              cardTemplates: [{ name: 'Card 1' }],
            },
            {
              id: 'nt-2',
              name: 'Cloze',
              fields: [{ name: 'Text' }],
              cardTemplates: [],
            },
          ],
        },
      },
      isPending: false,
      isError: false,
    } as never)

    await renderDialog()

    expect(screen.getByText('Basic')).toBeInTheDocument()
    expect(screen.getByText('Fields: 2 · Card templates: 1')).toBeInTheDocument()
    expect(screen.getByText('Cloze')).toBeInTheDocument()
    expect(screen.getByText('Fields: 1 · Card templates: 0')).toBeInTheDocument()
  })
})
