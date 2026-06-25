import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'

import { useAuthStore } from '../auth/authStore'
import { useDeckServiceListDecks } from '../../lib/api/collection/deck-service/deck-service'
import type { V1ListDecksResponse } from '../../lib/api/collection/collectionV1CollectionProto.schemas'
import { Button, EmptyState, Skeleton, Spinner } from '../../ui'
import { DeckMenu } from './DeckMenu'
import { DeckRow } from './DeckRow'
import { NewDeck } from './NewDeck'
import { QuickAddNote } from './QuickAddNote'

// Home / deck-picker (Task 4, see task-4-brief.md; redesigned onto the design system
// in Task 5, see task-5-brief.md). Replaces the Task 3 placeholder HomeScreen: lists
// the user's decks with per-deck study counts, each linking to /study/{deckId}.
export function DecksScreen() {
  const { t } = useTranslation('decks')
  const { t: tAuthoring } = useTranslation('authoring')
  const logout = useAuthStore((state) => state.logout)
  const { data, isPending } = useDeckServiceListDecks()
  // Tracks which deck (if any) currently has its QuickAddNote form open. Only one
  // deck's "Add card" affordance is expanded at a time.
  const [openDeckId, setOpenDeckId] = useState<string | null>(null)
  // Controls the create-deck Dialog (see NewDeck.tsx for the inline-vs-dialog
  // judgment call). Opened from the header button or the empty-state action.
  const [isNewDeckOpen, setIsNewDeckOpen] = useState(false)

  // The generated response type is a `{status: 200, data: T} | {status: <error>,
  // data: RpcStatus}` union, but `customFetch` already throws on any non-2xx
  // response (see mutator.ts) — by the time `data` is defined here it is always
  // the success shape (same invariant authStore.ts's `unwrap` documents).
  const items = (data?.data as V1ListDecksResponse | undefined)?.items ?? []
  const decks = items.filter(
    (
      deck: NonNullable<V1ListDecksResponse['items']>[number],
    ): deck is { id: string; name?: string } =>
      // `V1Deck.id` is optional in the generated type, but a deck without an id can't
      // be linked to or queried for counts — skip it rather than risk a duplicate
      // React key / a broken `/study/undefined` link.
      Boolean(deck.id),
  )

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between gap-3">
        <h1 className="text-xl font-semibold text-fg">{t('title')}</h1>
        <div className="flex items-center gap-2">
          <Button size="sm" onClick={() => setIsNewDeckOpen(true)}>
            {tAuthoring('newDeck.submit')}
          </Button>
          <Button variant="secondary" size="sm" onClick={() => void logout()}>
            {t('logout')}
          </Button>
        </div>
      </div>

      <NewDeck open={isNewDeckOpen} onClose={() => setIsNewDeckOpen(false)} />

      {isPending ? (
        <div className="flex flex-col gap-2" aria-busy="true">
          <Spinner label={t('loading')} />
          <Skeleton className="h-14 w-full" />
          <Skeleton className="h-14 w-full" />
        </div>
      ) : decks.length === 0 ? (
        <EmptyState
          title={t('empty')}
          description={t('emptyDescription')}
          action={
            <Button onClick={() => setIsNewDeckOpen(true)}>{tAuthoring('newDeck.submit')}</Button>
          }
        />
      ) : (
        <ul className="flex flex-col gap-2">
          {decks.map((deck) => {
            const isOpen = openDeckId === deck.id
            return (
              // The Link and the "Add card"/overflow-menu controls must be siblings,
              // not parent/child (see task-7-brief.md): nesting an interactive
              // button/form inside the deck-row Link is both an a11y violation and
              // would fire unwanted navigation on click.
              <li key={deck.id} className="flex flex-col gap-2">
                <div className="flex items-center gap-2">
                  <Link
                    to={`/study/${deck.id}`}
                    className="flex-1 rounded-[var(--radius-md)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-bg"
                  >
                    <DeckRow deckId={deck.id} name={deck.name ?? ''} />
                  </Link>
                  <DeckMenu
                    deckName={deck.name ?? ''}
                    onAddCard={() => setOpenDeckId(isOpen ? null : deck.id)}
                  />
                </div>
                {isOpen && <QuickAddNote deckId={deck.id} />}
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
