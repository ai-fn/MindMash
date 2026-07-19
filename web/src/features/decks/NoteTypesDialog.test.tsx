import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import '../../lib/i18n.ts'

vi.mock('../../lib/api/collection/note-type-service/note-type-service', () => ({
  useNoteTypeServiceListNoteTypes: vi.fn(),
  useNoteTypeServiceUpdateNoteType: vi.fn(),
  getNoteTypeServiceListNoteTypesQueryKey: vi.fn(() => ['/v1/note-types']),
}))

const mutateMock = vi.fn()

beforeEach(async () => {
  vi.clearAllMocks()
  const { useNoteTypeServiceUpdateNoteType } =
    await import('../../lib/api/collection/note-type-service/note-type-service')
  vi.mocked(useNoteTypeServiceUpdateNoteType).mockReturnValue({
    mutate: mutateMock,
    isPending: false,
  } as never)
})

async function renderDialog(open = true, onClose = vi.fn()) {
  const { NoteTypesDialog } = await import('./NoteTypesDialog.tsx')
  render(
    <QueryClientProvider client={new QueryClient()}>
      <NoteTypesDialog open={open} onClose={onClose} />
    </QueryClientProvider>,
  )
}

async function mockList(value: unknown) {
  const { useNoteTypeServiceListNoteTypes } =
    await import('../../lib/api/collection/note-type-service/note-type-service')
  vi.mocked(useNoteTypeServiceListNoteTypes).mockReturnValue(value as never)
}

describe('NoteTypesDialog', () => {
  it('renders nothing when closed', async () => {
    await mockList({ data: undefined, isPending: true, isError: false })
    await renderDialog(false)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('shows a loading state while the list is pending', async () => {
    await mockList({ data: undefined, isPending: true, isError: false })
    await renderDialog()
    expect(screen.getByRole('status', { name: 'Loading note types…' })).toBeInTheDocument()
  })

  it('shows an error state when the list fails', async () => {
    await mockList({ data: undefined, isPending: false, isError: true })
    await renderDialog()
    expect(screen.getByRole('alert')).toHaveTextContent('Something went wrong. Try again.')
  })

  it('shows an empty state when there are no note types', async () => {
    await mockList({ data: { data: { items: [] } }, isPending: false, isError: false })
    await renderDialog()
    expect(screen.getByText('No note types yet.')).toBeInTheDocument()
  })

  it('renders each note type with its field and template counts', async () => {
    await mockList({
      data: {
        data: {
          items: [
            {
              id: 'nt-1',
              name: 'Basic',
              fields: [{ name: 'Front' }, { name: 'Back' }],
              cardTemplates: [{ name: 'Card 1' }],
            },
            { id: 'nt-2', name: 'Cloze', fields: [{ name: 'Text' }], cardTemplates: [] },
          ],
        },
      },
      isPending: false,
      isError: false,
    })
    await renderDialog()
    expect(screen.getByText('Basic')).toBeInTheDocument()
    expect(screen.getByText('Fields: 2 · Card templates: 1')).toBeInTheDocument()
    expect(screen.getByText('Cloze')).toBeInTheDocument()
    expect(screen.getByText('Fields: 1 · Card templates: 0')).toBeInTheDocument()
  })

  it('shows each field language selector reflecting the saved value', async () => {
    await mockList({
      data: {
        data: {
          items: [
            {
              id: 'nt-1',
              name: 'CN',
              fields: [{ name: 'Hanzi', language: 'zh' }, { name: 'Gloss' }],
              cardTemplates: [{ name: 'Card 1' }],
              usn: '7',
            },
          ],
        },
      },
      isPending: false,
      isError: false,
    })
    await renderDialog()

    // The zh-tagged field shows Chinese; the untagged field shows the Auto default.
    expect(screen.getByRole('combobox', { name: 'Hanzi' })).toHaveValue('zh')
    expect(screen.getByRole('combobox', { name: 'Gloss' })).toHaveValue('')
  })

  it('persists a language change with the other fields preserved', async () => {
    await mockList({
      data: {
        data: {
          items: [
            {
              id: 'nt-1',
              name: 'CN',
              fields: [
                { name: 'Hanzi', sticky: true },
                { name: 'Gloss', rtl: true },
              ],
              cardTemplates: [{ name: 'Card 1' }],
              css: '.card{}',
              usn: '7',
            },
          ],
        },
      },
      isPending: false,
      isError: false,
    })
    await renderDialog()

    fireEvent.change(screen.getByRole('combobox', { name: 'Hanzi' }), {
      target: { value: 'zh' },
    })

    expect(mutateMock).toHaveBeenCalledTimes(1)
    const [vars] = mutateMock.mock.calls[0]
    expect(vars).toEqual({
      id: 'nt-1',
      data: {
        name: 'CN',
        // Only Hanzi's language changes; sticky/rtl on both fields are preserved.
        fields: [
          { name: 'Hanzi', sticky: true, language: 'zh' },
          { name: 'Gloss', rtl: true },
        ],
        cardTemplates: [{ name: 'Card 1' }],
        css: '.card{}',
        expectedUsn: '7',
      },
    })
  })
})
