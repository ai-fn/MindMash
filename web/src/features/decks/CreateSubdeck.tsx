import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { useQueryClient } from '@tanstack/react-query'

import {
  getDeckServiceListDecksQueryKey,
  useDeckServiceCreateDeck,
} from '../../lib/api/collection/deck-service/deck-service'
import { Button, Dialog, Field, TextInput, toast } from '../../ui'

export interface CreateSubdeckProps {
  /** The id of the deck the new subdeck will be parented under, or `null` when closed. */
  parentDeckId: string | null
  parentDeckName: string
  onClose: () => void
}

// Create-subdeck flow (Task 11a, see task-11a-brief.md). Structurally a clone of
// NewDeck.tsx's create-deck Dialog (Constraint 11 — same action, same shape), with the
// one difference being `parentId` is fixed to the deck the menu was opened from rather
// than user-entered. The dialog title names the parent deck so it's unambiguous which
// deck the new subdeck will nest under (Constraint 5 — no silent/ambiguous state).
export function CreateSubdeck({ parentDeckId, parentDeckName, onClose }: CreateSubdeckProps) {
  const { t } = useTranslation('authoring')
  const queryClient = useQueryClient()
  const createDeck = useDeckServiceCreateDeck()
  const [name, setName] = useState('')
  const [error, setError] = useState<string | null>(null)

  function handleClose() {
    setName('')
    setError(null)
    onClose()
  }

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!parentDeckId) {
      return
    }
    setError(null)
    createDeck.mutate(
      { data: { name, parentId: parentDeckId } },
      {
        onSuccess: () => {
          setName('')
          void queryClient.invalidateQueries({ queryKey: getDeckServiceListDecksQueryKey() })
          toast({ title: t('createSubdeck.success'), tone: 'success' })
          onClose()
        },
        onError: () => {
          setError(t('error'))
        },
      },
    )
  }

  const isDisabled = name.trim().length === 0 || createDeck.isPending

  return (
    <Dialog
      open={parentDeckId !== null}
      onClose={handleClose}
      title={t('createSubdeck.dialogTitle', { parentDeckName })}
    >
      <form onSubmit={handleSubmit} className="flex flex-col gap-4">
        <Field label={t('createSubdeck.label')}>
          {(controlProps) => (
            <TextInput
              {...controlProps}
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder={t('newDeck.placeholder')}
              autoFocus
            />
          )}
        </Field>

        {error ? (
          <p role="alert" className="text-sm text-danger">
            {error}
          </p>
        ) : null}

        <Button type="submit" disabled={isDisabled} loading={createDeck.isPending}>
          {t('createSubdeck.submit')}
        </Button>
      </form>
    </Dialog>
  )
}
