import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { useQueryClient } from '@tanstack/react-query'

import {
  getDeckServiceListDecksQueryKey,
  useDeckServiceUpdateDeck,
} from '../../lib/api/collection/deck-service/deck-service'
import type { V1Deck } from '../../lib/api/collection/collectionV1CollectionProto.schemas'
import { Button, Dialog, Field, TextInput, toast } from '../../ui'

export interface RenameDeckProps {
  /** The deck to rename, or `null` to render a closed Dialog. */
  deck: V1Deck | null
  onClose: () => void
}

// Rename-deck flow (Task 11a, see task-11a-brief.md). A Dialog rather than an inline
// editable field, matching NewDeck.tsx's create-deck convention closely since the two
// flows are structurally identical (single text field, single submit). Keeping the
// same shape for "create" and "rename" satisfies Constraint 11 (same action → same
// presentation), instead of inventing a second "lighter-weight" inline-rename pattern
// for no real benefit.
//
// UpdateDeck's body documents that it "replaces the full desired state of the deck"
// (see DeckServiceUpdateDeckBody's doc comment) — so the request carries the deck's
// current `parentId`/`configId` alongside the edited `name`, not just `{ name }`,
// to avoid accidentally clearing those fields. `expectedUsn` is read fresh from the
// `deck` prop (not a value captured at an earlier render) to avoid a stale-closure
// optimistic-concurrency mismatch, matching CardDetail's `note.usn` discipline.
export function RenameDeck({ deck, onClose }: RenameDeckProps) {
  const { t } = useTranslation('decks')
  const queryClient = useQueryClient()
  const updateDeck = useDeckServiceUpdateDeck()
  const [name, setName] = useState(deck?.name ?? '')
  const [seededForDeckId, setSeededForDeckId] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  // Re-seed the local edit buffer whenever a different deck is opened, mirroring
  // CardDetail's render-time seeding pattern (rather than a setState-in-effect).
  if (deck?.id && deck.id !== seededForDeckId) {
    setName(deck.name ?? '')
    setSeededForDeckId(deck.id)
  }

  function handleClose() {
    setSeededForDeckId(null)
    setError(null)
    onClose()
  }

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!deck?.id) {
      return
    }
    setError(null)
    // deck.usn is `string | undefined` on the wire type but always populated for a
    // persisted deck, same as `id` — not additionally guarded; see the invariant note
    // above CardDetail.tsx's handleSave().
    updateDeck.mutate(
      {
        id: deck.id,
        data: {
          name,
          parentId: deck.parentId,
          configId: deck.configId,
          expectedUsn: deck.usn,
        },
      },
      {
        onSuccess: () => {
          void queryClient.invalidateQueries({ queryKey: getDeckServiceListDecksQueryKey() })
          toast({ title: t('rename.success'), tone: 'success' })
          handleClose()
        },
        onError: () => {
          setError(t('error'))
        },
      },
    )
  }

  const isDisabled = name.trim().length === 0 || updateDeck.isPending

  return (
    <Dialog open={deck !== null} onClose={handleClose} title={t('rename.dialogTitle')}>
      <form onSubmit={handleSubmit} className="flex flex-col gap-4">
        <Field label={t('rename.label')}>
          {(controlProps) => (
            <TextInput
              {...controlProps}
              value={name}
              onChange={(event) => setName(event.target.value)}
              autoFocus
            />
          )}
        </Field>

        {error ? (
          <p role="alert" className="text-sm text-danger">
            {error}
          </p>
        ) : null}

        <Button type="submit" disabled={isDisabled} loading={updateDeck.isPending}>
          {t('rename.submit')}
        </Button>
      </form>
    </Dialog>
  )
}
