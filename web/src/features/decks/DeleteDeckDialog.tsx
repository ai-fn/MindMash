import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useQueryClient } from '@tanstack/react-query'

import {
  getDeckServiceListDecksQueryKey,
  useDeckServiceDeleteDeck,
} from '../../lib/api/collection/deck-service/deck-service'
import type { V1Deck } from '../../lib/api/collection/collectionV1CollectionProto.schemas'
import { Button, Dialog, toast } from '../../ui'

export interface DeleteDeckDialogProps {
  /** The deck to delete, or `null` to render a closed Dialog. */
  deck: V1Deck | null
  onClose: () => void
}

// Delete-deck flow (Task 11a, see task-11a-brief.md). Genuine two-step confirm
// (Constraint 8 — destructive actions need confirmation): opening this Dialog from the
// menu is step one (an explicit, deliberate click on "Delete"), and the Dialog itself
// shows only a confirm/cancel pair as step two — directly mirroring CardDetail.tsx's
// `confirmingDelete`-gated confirm UI, just hosted in its own Dialog instead of an
// in-place panel, since DeckMenu (the opener) is a transient popup that closes on
// outside-click and can't itself host a multi-step confirm (see DeckMenu.tsx's comment).
//
// No backend undo/restore exists for decks (confirmed against DeckService's contract,
// same as notes/cards elsewhere in this codebase) — success is an ordinary toast, not a
// fabricated "undo" affordance.
export function DeleteDeckDialog({ deck, onClose }: DeleteDeckDialogProps) {
  const { t } = useTranslation('decks')
  const queryClient = useQueryClient()
  const deleteDeck = useDeckServiceDeleteDeck()
  const [error, setError] = useState<string | null>(null)

  function handleClose() {
    setError(null)
    onClose()
  }

  function handleConfirm() {
    if (!deck?.id) {
      return
    }
    setError(null)
    // deck.usn: always populated for a persisted deck; see the invariant note above
    // CardDetail.tsx's handleSave().
    deleteDeck.mutate(
      { id: deck.id, params: { expectedUsn: deck.usn } },
      {
        onSuccess: () => {
          void queryClient.invalidateQueries({ queryKey: getDeckServiceListDecksQueryKey() })
          toast({ title: t('delete.success'), tone: 'success' })
          handleClose()
        },
        onError: () => {
          setError(t('error'))
        },
      },
    )
  }

  return (
    <Dialog open={deck !== null} onClose={handleClose} title={t('delete.dialogTitle')}>
      <div className="flex flex-col gap-4">
        <p className="text-sm text-fg">{t('delete.confirm', { deckName: deck?.name ?? '' })}</p>

        {error ? (
          <p role="alert" className="text-sm text-danger">
            {error}
          </p>
        ) : null}

        <div className="flex items-center justify-between gap-3">
          <Button variant="secondary" onClick={handleClose} disabled={deleteDeck.isPending}>
            {t('delete.cancel')}
          </Button>
          <Button
            variant="danger"
            onClick={handleConfirm}
            loading={deleteDeck.isPending}
            disabled={deleteDeck.isPending}
          >
            {t('delete.confirmAction')}
          </Button>
        </div>
      </div>
    </Dialog>
  )
}
