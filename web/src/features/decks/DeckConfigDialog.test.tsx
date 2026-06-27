import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import '../../lib/i18n.ts'

vi.mock('../../lib/api/collection/deck-service/deck-service', () => ({
  useDeckServiceGetDeckConfig: vi.fn(),
  useDeckServiceUpdateDeckConfig: vi.fn(),
  getDeckServiceGetDeckConfigQueryKey: vi.fn((id: string) => [`/v1/deck-configs/${id}`]),
}))

const TOAST_MOCK = vi.fn()
vi.mock('../../ui', async () => {
  const actual = await vi.importActual<typeof import('../../ui')>('../../ui')
  return { ...actual, toast: TOAST_MOCK }
})

function makeDeck(overrides: Record<string, unknown> = {}) {
  return {
    id: 'deck-1',
    name: 'Spanish',
    configId: 'config-1',
    ...overrides,
  }
}

function makeConfig(overrides: Record<string, unknown> = {}) {
  return {
    id: 'config-1',
    name: 'Default',
    newPerDay: 20,
    revPerDay: 200,
    desiredRetention: 0.9,
    fsrsWeights: [1, 2, 3],
    learningSteps: [60, 600],
    rolloverHour: 4,
    timezone: 'UTC',
    usn: 'usn-cfg-1',
    ...overrides,
  }
}

async function setupHooks({
  config = makeConfig(),
  configPending = false,
  configError = false,
  updateMutate = vi.fn(),
  updatePending = false,
}: {
  config?: ReturnType<typeof makeConfig> | undefined
  configPending?: boolean
  configError?: boolean
  updateMutate?: ReturnType<typeof vi.fn>
  updatePending?: boolean
} = {}) {
  const { useDeckServiceGetDeckConfig, useDeckServiceUpdateDeckConfig } =
    await import('../../lib/api/collection/deck-service/deck-service')

  vi.mocked(useDeckServiceGetDeckConfig).mockReturnValue({
    data: config ? { data: { deckConfig: config } } : undefined,
    isPending: configPending,
    isError: configError,
  } as never)

  vi.mocked(useDeckServiceUpdateDeckConfig).mockReturnValue({
    mutate: updateMutate,
    isPending: updatePending,
  } as never)

  return { updateMutate }
}

async function renderConfigDialog(
  deck: ReturnType<typeof makeDeck> | null = makeDeck(),
  onClose = vi.fn(),
) {
  const { DeckConfigDialog } = await import('./DeckConfigDialog.tsx')
  const queryClient = new QueryClient()
  const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
  render(
    <QueryClientProvider client={queryClient}>
      <DeckConfigDialog deck={deck} onClose={onClose} />
    </QueryClientProvider>,
  )
  return { invalidateSpy }
}

describe('DeckConfigDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.resetModules()
  })

  it('renders nothing when deck is null', async () => {
    await setupHooks()
    await renderConfigDialog(null)

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('shows an unavailable empty state when the deck has no configId', async () => {
    await setupHooks({ config: undefined })
    await renderConfigDialog(makeDeck({ configId: undefined }))

    expect(screen.getByText('Settings unavailable for this deck.')).toBeInTheDocument()
  })

  it('loads the config and pre-fills desiredRetention as a whole-number percentage', async () => {
    await setupHooks({ config: makeConfig({ desiredRetention: 0.87 }) })
    await renderConfigDialog()

    expect(screen.getByDisplayValue('87')).toBeInTheDocument()
    expect(screen.getByDisplayValue('20')).toBeInTheDocument()
    expect(screen.getByDisplayValue('200')).toBeInTheDocument()
  })

  it('submits UpdateDeckConfig converting the edited percentage back to a 0..1 float, with other fields preserved and the fresh expectedUsn', async () => {
    const updateMutate = vi.fn((_vars, opts: { onSuccess?: () => void }) => {
      opts.onSuccess?.()
    })
    await setupHooks({
      config: makeConfig({ desiredRetention: 0.9, usn: 'usn-cfg-9' }),
      updateMutate,
    })
    const user = userEvent.setup()
    const onClose = vi.fn()

    const { invalidateSpy } = await renderConfigDialog(makeDeck(), onClose)

    const retentionInput = screen.getByDisplayValue('90')
    await user.clear(retentionInput)
    await user.type(retentionInput, '95')

    const newPerDayInput = screen.getByDisplayValue('20')
    await user.clear(newPerDayInput)
    await user.type(newPerDayInput, '25')

    await user.click(screen.getByRole('button', { name: 'Save settings' }))

    expect(updateMutate).toHaveBeenCalledWith(
      {
        id: 'config-1',
        data: {
          name: 'Default',
          desiredRetention: 0.95,
          newPerDay: 25,
          revPerDay: 200,
          fsrsWeights: [1, 2, 3],
          learningSteps: [60, 600],
          rolloverHour: 4,
          timezone: 'UTC',
          expectedUsn: 'usn-cfg-9',
        },
      },
      expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
    )

    await waitFor(() => {
      expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['/v1/deck-configs/config-1'] })
    })
    expect(onClose).toHaveBeenCalledTimes(1)
    expect(TOAST_MOCK).toHaveBeenCalledWith(
      expect.objectContaining({ title: 'Deck settings saved.', tone: 'success' }),
    )
  })

  it('shows an error and stays open when UpdateDeckConfig fails', async () => {
    const updateMutate = vi.fn((_vars, opts: { onError?: () => void }) => {
      opts.onError?.()
    })
    await setupHooks({ updateMutate })
    const user = userEvent.setup()
    const onClose = vi.fn()

    await renderConfigDialog(makeDeck(), onClose)
    await user.click(screen.getByRole('button', { name: 'Save settings' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Something went wrong. Try again.')
    expect(onClose).not.toHaveBeenCalled()
    expect(TOAST_MOCK).not.toHaveBeenCalled()
  })
})
