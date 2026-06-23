import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { useQueryClient } from '@tanstack/react-query'

import {
  getDeckServiceListDecksQueryKey,
  useDeckServiceCreateDeck,
} from '../../lib/api/collection/deck-service/deck-service'

// Minimal inline create-deck form (Task 7, see task-7-brief.md). Finally satisfies
// the create-deck CTA Task 4 deferred — DecksScreen.tsx's empty-state branch used to
// note "Task 7 will add a create-deck action here".
export function NewDeck() {
  const { t } = useTranslation('authoring')
  const queryClient = useQueryClient()
  const createDeck = useDeckServiceCreateDeck()
  const [name, setName] = useState('')
  const [error, setError] = useState<string | null>(null)

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setError(null)
    createDeck.mutate(
      { data: { name } },
      {
        onSuccess: () => {
          setName('')
          void queryClient.invalidateQueries({ queryKey: getDeckServiceListDecksQueryKey() })
        },
        onError: () => {
          setError(t('error'))
        },
      },
    )
  }

  const isDisabled = name.trim().length === 0 || createDeck.isPending

  return (
    <form onSubmit={handleSubmit} className="flex items-end gap-2">
      <label className="flex flex-1 flex-col gap-1 text-sm text-slate-700">
        {t('newDeck.label')}
        <input
          type="text"
          value={name}
          onChange={(event) => setName(event.target.value)}
          placeholder={t('newDeck.placeholder')}
          className="rounded border border-slate-300 px-3 py-2"
        />
      </label>
      <button
        type="submit"
        disabled={isDisabled}
        className="rounded bg-slate-800 px-4 py-2 text-white disabled:opacity-50"
      >
        {t('newDeck.submit')}
      </button>
      {error && <p className="text-sm text-red-600">{error}</p>}
    </form>
  )
}
