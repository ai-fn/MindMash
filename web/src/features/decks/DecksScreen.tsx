import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'

import { useDeckServiceListDecks } from '../../lib/api/collection/deck-service/deck-service'
import type {
  V1Deck,
  V1ListDecksResponse,
} from '../../lib/api/collection/collectionV1CollectionProto.schemas'
import { Button, EmptyState, Skeleton, Spinner } from '../../ui'
import { CreateSubdeck } from './CreateSubdeck'
import { DeckConfigDialog } from './DeckConfigDialog'
import { DeckMenu } from './DeckMenu'
import { DeckRow } from './DeckRow'
import { DeleteDeckDialog } from './DeleteDeckDialog'
import { FilteredDecksDialog } from './FilteredDecksDialog'
import { NewDeck } from './NewDeck'
import { NoteTypesDialog } from './NoteTypesDialog'
import { QuickAddNote } from './QuickAddNote'
import { RenameDeck } from './RenameDeck'

// Home / deck-picker (Task 4, see task-4-brief.md; redesigned onto the design system
// in Task 5, see task-5-brief.md). Replaces the Task 3 placeholder HomeScreen: lists
// the user's decks with per-deck study counts, each linking to /study/{deckId}.
export function DecksScreen() {
  const { t } = useTranslation('decks')
  const { t: tAuthoring } = useTranslation('authoring')
  const { data, isPending } = useDeckServiceListDecks()
  // Tracks which deck (if any) currently has its QuickAddNote form open. Only one
  // deck's "Add card" affordance is expanded at a time.
  const [openDeckId, setOpenDeckId] = useState<string | null>(null)
  // Controls the create-deck Dialog (see NewDeck.tsx for the inline-vs-dialog
  // judgment call). Opened from the header button or the empty-state action.
  const [isNewDeckOpen, setIsNewDeckOpen] = useState(false)
  // Task 11a: per-deck dialogs (rename/delete/create-subdeck/config), each tracking
  // "which deck is this open for" rather than a plain boolean — these dialogs live
  // here (not inside DeckMenu) because DeckMenu is a transient popup that closes on
  // outside-click/Escape, and a confirm-delete or multi-field config form needs to
  // survive that (see DeckMenu.tsx's comment for the full rationale).
  const [renamingDeck, setRenamingDeck] = useState<V1Deck | null>(null)
  const [deletingDeck, setDeletingDeck] = useState<V1Deck | null>(null)
  const [subdeckParent, setSubdeckParent] = useState<V1Deck | null>(null)
  const [configDeck, setConfigDeck] = useState<V1Deck | null>(null)
  // Task 11b: note-types and filtered-decks dialogs are not per-deck — plain booleans
  // are enough (unlike the V1Deck-keyed trackers above), opened from their own header
  // buttons rather than DeckMenu.
  const [isNoteTypesOpen, setIsNoteTypesOpen] = useState(false)
  const [isFilteredDecksOpen, setIsFilteredDecksOpen] = useState(false)

  // The generated response type is a `{status: 200, data: T} | {status: <error>,
  // data: RpcStatus}` union, but `customFetch` already throws on any non-2xx
  // response (see mutator.ts) — by the time `data` is defined here it is always
  // the success shape (same invariant authStore.ts's `unwrap` documents).
  const items = (data?.data as V1ListDecksResponse | undefined)?.items ?? []
  const decks = items.filter((deck: V1Deck): deck is V1Deck & { id: string } =>
    // `V1Deck.id` is optional in the generated type, but a deck without an id can't
    // be linked to or queried for counts — skip it rather than risk a duplicate
    // React key / a broken `/study/undefined` link.
    Boolean(deck.id),
  )

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold text-fg">{t('title')}</h1>
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="secondary" size="sm" onClick={() => setIsNoteTypesOpen(true)}>
            {t('noteTypes.openButton')}
          </Button>
          <Button variant="secondary" size="sm" onClick={() => setIsFilteredDecksOpen(true)}>
            {t('filteredDecks.openButton')}
          </Button>
          <Button size="sm" onClick={() => setIsNewDeckOpen(true)}>
            {tAuthoring('newDeck.submit')}
          </Button>
        </div>
      </div>

      <NewDeck open={isNewDeckOpen} onClose={() => setIsNewDeckOpen(false)} />
      <RenameDeck deck={renamingDeck} onClose={() => setRenamingDeck(null)} />
      <DeleteDeckDialog deck={deletingDeck} onClose={() => setDeletingDeck(null)} />
      <CreateSubdeck
        parentDeckId={subdeckParent?.id ?? null}
        parentDeckName={subdeckParent?.name ?? ''}
        onClose={() => setSubdeckParent(null)}
      />
      <DeckConfigDialog deck={configDeck} onClose={() => setConfigDeck(null)} />
      <NoteTypesDialog open={isNoteTypesOpen} onClose={() => setIsNoteTypesOpen(false)} />
      <FilteredDecksDialog
        open={isFilteredDecksOpen}
        onClose={() => setIsFilteredDecksOpen(false)}
      />

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
                    onRename={() => setRenamingDeck(deck)}
                    onDelete={() => setDeletingDeck(deck)}
                    onCreateSubdeck={() => setSubdeckParent(deck)}
                    onEditConfig={() => setConfigDeck(deck)}
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
