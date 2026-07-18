import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import '../../lib/i18n.ts'

// Mock the generated collection hooks the bar consumes.
vi.mock('../../lib/api/collection/card-service/card-service', () => ({
  useCardServiceBulkCards: vi.fn(),
}))
vi.mock('../../lib/api/collection/deck-service/deck-service', () => ({
  useDeckServiceListDecks: vi.fn(),
}))

// Stub only `toast` out of the ui barrel; keep the real primitives (Dialog/Select/…).
const toastMock = vi.fn()
vi.mock('../../ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../ui')>()),
  toast: toastMock,
}))

// A mutate mock that immediately resolves via onSuccess with a canned affectedCount, so both
// the request payload and the success side-effects (toast + onDone) can be asserted in one go.
function mutateSucceeding(affectedCount = '2') {
  return vi.fn((_vars: unknown, opts?: { onSuccess?: (res: unknown) => void }) => {
    opts?.onSuccess?.({ data: { affectedCount }, status: 200, headers: new Headers() })
  })
}

async function renderBar(selectedIds = ['c1', 'c2'], onDone = vi.fn()) {
  const { BulkActionBar } = await import('./BulkActionBar.tsx')
  const queryClient = new QueryClient()
  render(
    <QueryClientProvider client={queryClient}>
      <BulkActionBar selectedIds={selectedIds} onDone={onDone} />
    </QueryClientProvider>,
  )
  return { onDone }
}

async function setup() {
  const { useCardServiceBulkCards } =
    await import('../../lib/api/collection/card-service/card-service')
  const { useDeckServiceListDecks } =
    await import('../../lib/api/collection/deck-service/deck-service')
  const mutate = mutateSucceeding()
  vi.mocked(useCardServiceBulkCards).mockReturnValue({ mutate, isPending: false } as never)
  vi.mocked(useDeckServiceListDecks).mockReturnValue({
    data: { data: { items: [{ id: 'deck-9', path: 'French::Verbs' }] } },
    isPending: false,
  } as never)
  return { mutate }
}

describe('BulkActionBar', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })
  afterEach(() => {
    vi.resetModules()
  })

  it('suspend applies directly with the selected IDs and no args, then toasts + calls onDone', async () => {
    const { mutate } = await setup()
    const user = userEvent.setup()
    const { onDone } = await renderBar(['c1', 'c2'])

    await user.click(screen.getByRole('button', { name: 'Suspend' }))

    expect(mutate).toHaveBeenCalledTimes(1)
    expect(mutate.mock.calls[0][0]).toEqual({
      data: { idsList: { ids: ['c1', 'c2'] }, op: 'BULK_OP_SUSPEND', args: undefined },
    })
    expect(toastMock).toHaveBeenCalledWith(
      expect.objectContaining({ tone: 'success', title: expect.stringContaining('2') }),
    )
    expect(onDone).toHaveBeenCalledTimes(1)
  })

  it('delete confirms in a dialog before applying BULK_OP_DELETE', async () => {
    const { mutate } = await setup()
    const user = userEvent.setup()
    await renderBar(['c1'])

    await user.click(screen.getByRole('button', { name: 'Delete' }))
    const dialog = screen.getByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }))

    expect(mutate.mock.calls[0][0]).toEqual({
      data: { idsList: { ids: ['c1'] }, op: 'BULK_OP_DELETE', args: undefined },
    })
  })

  it('tag sends BULK_OP_TAG with the typed tag; untag sends BULK_OP_UNTAG', async () => {
    const { mutate } = await setup()
    const user = userEvent.setup()
    await renderBar(['c1'])

    await user.click(screen.getByRole('button', { name: 'Tag' }))
    await user.type(within(screen.getByRole('dialog')).getByRole('textbox'), 'grammar')
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Add tag' }))

    expect(mutate.mock.calls[0][0]).toEqual({
      data: { idsList: { ids: ['c1'] }, op: 'BULK_OP_TAG', args: { tag: 'grammar' } },
    })

    await user.click(screen.getByRole('button', { name: 'Untag' }))
    await user.type(within(screen.getByRole('dialog')).getByRole('textbox'), 'grammar')
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Remove tag' }))

    expect(mutate.mock.calls[1][0]).toEqual({
      data: { idsList: { ids: ['c1'] }, op: 'BULK_OP_UNTAG', args: { tag: 'grammar' } },
    })
  })

  it('flag sends BULK_OP_FLAG with the chosen colour value', async () => {
    const { mutate } = await setup()
    const user = userEvent.setup()
    await renderBar(['c1'])

    await user.click(screen.getByRole('button', { name: 'Flag' }))
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Red' }))

    expect(mutate.mock.calls[0][0]).toEqual({
      data: { idsList: { ids: ['c1'] }, op: 'BULK_OP_FLAG', args: { flag: 1 } },
    })
  })

  it('move sends BULK_OP_MOVE with the picked deck id', async () => {
    const { mutate } = await setup()
    const user = userEvent.setup()
    await renderBar(['c1'])

    await user.click(screen.getByRole('button', { name: 'Move' }))
    const dialog = screen.getByRole('dialog')
    await user.selectOptions(within(dialog).getByRole('combobox'), 'deck-9')
    await user.click(within(dialog).getByRole('button', { name: 'Move' }))

    expect(mutate.mock.calls[0][0]).toEqual({
      data: { idsList: { ids: ['c1'] }, op: 'BULK_OP_MOVE', args: { targetDeckId: 'deck-9' } },
    })
  })
})
