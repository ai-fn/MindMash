import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'

import { useAuthStore } from '../auth/authStore'
import { useDeckServiceListDecks } from '../../lib/api/collection/deck-service/deck-service'
import type { V1ListDecksResponse } from '../../lib/api/collection/collectionV1CollectionProto.schemas'
import { DeckRow } from './DeckRow'
import { NewDeck } from './NewDeck'
import { QuickAddNote } from './QuickAddNote'

// Home / deck-picker (Task 4, see task-4-brief.md). Replaces the Task 3 placeholder
// HomeScreen: lists the user's decks with per-deck study counts, each linking to
// /study/{deckId} (Task 6's route — not stubbed here, see brief judgment call 4).
export function DecksScreen() {
  const { t } = useTranslation('decks')
  const { t: tAuthoring } = useTranslation('authoring')
  const logout = useAuthStore((state) => state.logout)
  const { data, isPending } = useDeckServiceListDecks()
  // Tracks which deck (if any) currently has its QuickAddNote form open. Only one
  // deck's "Add card" affordance is expanded at a time.
  const [openDeckId, setOpenDeckId] = useState<string | null>(null)

  // The generated response type is a `{status: 200, data: T} | {status: <error>,
  // data: RpcStatus}` union, but `customFetch` already throws on any non-2xx
  // response (see mutator.ts) — by the time `data` is defined here it is always
  // the success shape (same invariant authStore.ts's `unwrap` documents).
  const items = (data?.data as V1ListDecksResponse | undefined)?.items ?? []

  return (
    <main className="flex min-h-screen flex-col gap-6 bg-slate-50 p-6">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold text-slate-800">{t('title')}</h1>
        <button
          type="button"
          onClick={() => void logout()}
          className="rounded bg-slate-800 px-4 py-2 text-white"
        >
          {t('logout')}
        </button>
      </div>

      <NewDeck />

      {isPending ? (
        <p className="text-slate-600">{t('loading')}</p>
      ) : items.length === 0 ? (
        <p className="text-slate-600">{t('empty')}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {items
            // `V1Deck.id` is optional in the generated type, but a deck without an id
            // can't be linked to or queried for counts — skip it rather than risk a
            // duplicate React key / a broken `/study/undefined` link.
            .filter(
              (
                deck: NonNullable<V1ListDecksResponse['items']>[number],
              ): deck is { id: string; name?: string } => Boolean(deck.id),
            )
            .map((deck) => {
              const isOpen = openDeckId === deck.id
              return (
                // The Link and the "Add card" button must be siblings, not
                // parent/child (see task-7-brief.md): nesting an interactive
                // button/form inside the deck-row Link is both an a11y violation
                // and would fire unwanted navigation on click.
                <li key={deck.id} className="flex flex-col gap-2">
                  <div className="flex items-center gap-2">
                    <Link to={`/study/${deck.id}`} className="flex-1">
                      <DeckRow deckId={deck.id} name={deck.name ?? ''} />
                    </Link>
                    <button
                      type="button"
                      onClick={() => setOpenDeckId(isOpen ? null : deck.id)}
                      className="rounded border border-slate-300 px-3 py-2 text-sm text-slate-700"
                    >
                      {tAuthoring('addCard.toggle')}
                    </button>
                  </div>
                  {isOpen && <QuickAddNote deckId={deck.id} />}
                </li>
              )
            })}
        </ul>
      )}
    </main>
  )
}
