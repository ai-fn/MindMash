import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { useQueryClient } from '@tanstack/react-query'

import {
  getDeckServiceGetDeckConfigQueryKey,
  useDeckServiceGetDeckConfig,
  useDeckServiceUpdateDeckConfig,
} from '../../lib/api/collection/deck-service/deck-service'
import type {
  V1Deck,
  V1GetDeckConfigResponse,
} from '../../lib/api/collection/collectionV1CollectionProto.schemas'
import { Button, Dialog, EmptyState, Field, Skeleton, Spinner, TextInput, toast } from '../../ui'

export interface DeckConfigDialogProps {
  /** The deck whose config is being edited, or `null` to render a closed Dialog. */
  deck: V1Deck | null
  onClose: () => void
}

/** Formats a 0..1 float as a whole-number percent string for the input, e.g. 0.9 -> "90". */
function toPercentString(value: number | undefined): string {
  if (value === undefined || !Number.isFinite(value)) {
    return ''
  }
  return String(Math.round(value * 100))
}

/** Parses a percent-string input back into a 0..1 float for the API, e.g. "90" -> 0.9. */
function fromPercentString(value: string): number | undefined {
  const n = Number(value)
  if (!Number.isFinite(n)) {
    return undefined
  }
  return n / 100
}

// Deck-config (scheduling settings) editor (Task 11a, see task-11a-brief.md). Opened
// from DeckMenu's "Deck settings" item — a separate menu entry rather than folded into
// rename, per Constraint 1 (one screen = one action): renaming a deck and tuning its
// FSRS scheduling parameters are unrelated decisions with different stakes (a typo in
// a deck name is trivial to fix; a botched retention target silently changes review
// load for every card in the deck), so they get separate single-purpose screens.
//
// Scope cut (confirmed in the task brief): edits exactly three fields —
// `desiredRetention` (rendered/edited as a 0-100 percentage, converted to/from the
// API's 0..1 float using the same precedent as StatsScreen.tsx's `toPercent`),
// `newPerDay`, `revPerDay`. `fsrsWeights`/`learningSteps`/`rolloverHour`/`timezone` are
// internal-tuning fields with no UI here — out of scope for this task.
//
// Missing-configId handling (judgment call, option (a) from the brief): if
// `deck.configId` is empty/undefined, this renders an "unavailable" empty state rather
// than silently creating a new config. Auto-creating-and-assigning a config as a side
// effect of merely opening a settings dialog is a surprising, non-obvious mutation
// (Constraint 5 — every action should give intentional, expected feedback, not a
// silent background write) — every deck created via CreateDeck.tsx's NewDeck/
// CreateSubdeck flows already always has a configId from the backend in normal
// operation, so this is a defensive fallback, not an expected path.
//
// Close-vs-stay-open after save (judgment call): the dialog closes on successful save,
// matching RenameDeck/CreateSubdeck/DeleteDeckDialog's "single action, then close"
// shape (Constraint 1) rather than treating config editing as a persistent settings
// panel.
export function DeckConfigDialog({ deck, onClose }: DeckConfigDialogProps) {
  const { t } = useTranslation('decks')
  const queryClient = useQueryClient()
  const configId = deck?.configId

  const configQuery = useDeckServiceGetDeckConfig(configId ?? '', {
    query: { enabled: !!configId },
  })
  const config = (configQuery.data?.data as V1GetDeckConfigResponse | undefined)?.deckConfig

  const updateConfig = useDeckServiceUpdateDeckConfig()

  const [seededForConfigId, setSeededForConfigId] = useState<string | null>(null)
  const [desiredRetentionPercent, setDesiredRetentionPercent] = useState('')
  const [newPerDay, setNewPerDay] = useState('')
  const [revPerDay, setRevPerDay] = useState('')
  const [error, setError] = useState<string | null>(null)

  // Seed (or re-seed, for a different config) the edit buffer once the config loads,
  // matching CardDetail's/RenameDeck's render-time seeding pattern.
  if (config?.id && config.id !== seededForConfigId) {
    setDesiredRetentionPercent(toPercentString(config.desiredRetention))
    setNewPerDay(String(config.newPerDay ?? ''))
    setRevPerDay(String(config.revPerDay ?? ''))
    setSeededForConfigId(config.id)
  }

  function handleClose() {
    setSeededForConfigId(null)
    setError(null)
    onClose()
  }

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!configId || !config) {
      return
    }
    setError(null)
    updateConfig.mutate(
      {
        id: configId,
        data: {
          name: config.name,
          desiredRetention: fromPercentString(desiredRetentionPercent),
          newPerDay: Number(newPerDay),
          revPerDay: Number(revPerDay),
          fsrsWeights: config.fsrsWeights,
          learningSteps: config.learningSteps,
          rolloverHour: config.rolloverHour,
          timezone: config.timezone,
          expectedUsn: config.usn,
        },
      },
      {
        onSuccess: () => {
          void queryClient.invalidateQueries({
            queryKey: getDeckServiceGetDeckConfigQueryKey(configId),
          })
          toast({ title: t('config.success'), tone: 'success' })
          handleClose()
        },
        onError: () => {
          setError(t('error'))
        },
      },
    )
  }

  const isDisabled =
    desiredRetentionPercent.trim().length === 0 ||
    newPerDay.trim().length === 0 ||
    revPerDay.trim().length === 0 ||
    updateConfig.isPending

  return (
    <Dialog open={deck !== null} onClose={handleClose} title={t('config.dialogTitle')}>
      {!configId ? (
        <EmptyState
          title={t('config.unavailable')}
          description={t('config.unavailableDescription')}
        />
      ) : configQuery.isPending ? (
        <div className="flex flex-col gap-2" aria-busy="true">
          <Spinner label={t('config.loading')} />
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-full" />
        </div>
      ) : configQuery.isError || !config ? (
        <p role="alert" className="text-sm text-danger">
          {t('error')}
        </p>
      ) : (
        <form onSubmit={handleSubmit} className="flex flex-col gap-4">
          <Field label={t('config.desiredRetentionLabel')}>
            {(controlProps) => (
              <TextInput
                {...controlProps}
                type="number"
                min={0}
                max={100}
                value={desiredRetentionPercent}
                onChange={(event) => setDesiredRetentionPercent(event.target.value)}
              />
            )}
          </Field>

          <Field label={t('config.newPerDayLabel')}>
            {(controlProps) => (
              <TextInput
                {...controlProps}
                type="number"
                min={0}
                value={newPerDay}
                onChange={(event) => setNewPerDay(event.target.value)}
              />
            )}
          </Field>

          <Field label={t('config.revPerDayLabel')}>
            {(controlProps) => (
              <TextInput
                {...controlProps}
                type="number"
                min={0}
                value={revPerDay}
                onChange={(event) => setRevPerDay(event.target.value)}
              />
            )}
          </Field>

          {error ? (
            <p role="alert" className="text-sm text-danger">
              {error}
            </p>
          ) : null}

          <Button type="submit" disabled={isDisabled} loading={updateConfig.isPending}>
            {t('config.submit')}
          </Button>
        </form>
      )}
    </Dialog>
  )
}
