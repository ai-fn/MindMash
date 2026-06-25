import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { useQueryClient } from '@tanstack/react-query'

import {
  getDeckServiceListDecksQueryKey,
  useDeckServiceCreateDeck,
} from '../../lib/api/collection/deck-service/deck-service'
import { Button, Dialog, Field, TextInput } from '../../ui'

export interface NewDeckProps {
  open: boolean
  onClose: () => void
}

// Create-deck flow (Task 7, see task-7-brief.md), restyled (Task 5) as a Dialog
// rather than an always-visible inline form. Judgment call: the deck list is the
// primary "home" view (Constraint 2 — minimal cognitive load), and create-deck is an
// occasional action, not something that needs permanent on-screen real estate. A
// Dialog keeps that view quiet and treats "create a deck" as its own single-purpose
// screen (Constraint 1) triggered from one consistent place (the header's "New deck"
// button, Constraint 11), while the empty state's action button opens the same
// Dialog instead of duplicating the form inline.
export function NewDeck({ open, onClose }: NewDeckProps) {
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
    setError(null)
    createDeck.mutate(
      { data: { name } },
      {
        onSuccess: () => {
          setName('')
          void queryClient.invalidateQueries({ queryKey: getDeckServiceListDecksQueryKey() })
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
    <Dialog open={open} onClose={handleClose} title={t('newDeck.dialogTitle')}>
      <form onSubmit={handleSubmit} className="flex flex-col gap-4">
        <Field label={t('newDeck.label')}>
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
          {t('newDeck.submit')}
        </Button>
      </form>
    </Dialog>
  )
}
