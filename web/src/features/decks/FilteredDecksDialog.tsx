import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { useQueryClient } from '@tanstack/react-query'

import {
  getFilteredDeckServiceListFilteredDecksQueryKey,
  useFilteredDeckServiceCreateFilteredDeck,
  useFilteredDeckServiceDeleteFilteredDeck,
  useFilteredDeckServiceListFilteredDecks,
} from '../../lib/api/collection/filtered-deck-service/filtered-deck-service'
import type { V1ListFilteredDecksResponse } from '../../lib/api/collection/collectionV1CollectionProto.schemas'
import { Button, Dialog, EmptyState, Field, Skeleton, Spinner, TextInput, toast } from '../../ui'

export interface FilteredDecksDialogProps {
  open: boolean
  onClose: () => void
}

// Filtered-decks manager (Task 11b, see task-11-brief.md): list + create + delete in
// one Dialog. Unlike Task 11a's per-deck dialogs (rename/delete/config, each its own
// Dialog keyed off "which deck"), list+create+delete for filtered decks are kept
// together here because a filtered deck is a single flat resource with no sub-actions
// beyond "make one" / "remove one" — splitting create and delete into separate dialogs
// would add navigation overhead without separating distinct concerns (Constraint 2 —
// minimal cognitive load). The two-step delete confirm still applies per-row
// (Constraint 8), it's just inline rather than a second nested Dialog, since a Dialog
// stacked on a Dialog is its own a11y/UX problem (focus-trap nesting) this design
// doesn't need to take on for a single confirm/cancel pair.
//
// Out of scope (per brief): `V1FilteredDeck.query` is a saved search-DSL string but is
// never executed/previewed here — no SearchService wiring. Update/"rebuild" of a
// filtered deck is also out of scope; this dialog only creates and deletes.
export function FilteredDecksDialog({ open, onClose }: FilteredDecksDialogProps) {
  const { t } = useTranslation('decks')
  const queryClient = useQueryClient()
  const listQuery = useFilteredDeckServiceListFilteredDecks(undefined, {
    query: { enabled: open },
  })
  const createFilteredDeck = useFilteredDeckServiceCreateFilteredDeck()
  const deleteFilteredDeck = useFilteredDeckServiceDeleteFilteredDeck()

  const [name, setName] = useState('')
  const [query, setQuery] = useState('')
  const [createError, setCreateError] = useState<string | null>(null)
  // Tracks which row (by id) is showing its delete confirm, step one of the two-step
  // confirm pattern (Constraint 8), mirroring DeleteDeckDialog.tsx's rationale.
  const [confirmingDeleteId, setConfirmingDeleteId] = useState<string | null>(null)
  const [deleteError, setDeleteError] = useState<string | null>(null)

  const items = (listQuery.data?.data as V1ListFilteredDecksResponse | undefined)?.items ?? []

  function handleClose() {
    setName('')
    setQuery('')
    setCreateError(null)
    setConfirmingDeleteId(null)
    setDeleteError(null)
    onClose()
  }

  function handleCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setCreateError(null)
    createFilteredDeck.mutate(
      { data: { name, query } },
      {
        onSuccess: () => {
          setName('')
          setQuery('')
          void queryClient.invalidateQueries({
            queryKey: getFilteredDeckServiceListFilteredDecksQueryKey(),
          })
          toast({ title: t('filteredDecks.createSuccess'), tone: 'success' })
        },
        onError: () => {
          setCreateError(t('error'))
        },
      },
    )
  }

  function handleConfirmDelete(id: string) {
    setDeleteError(null)
    // Read expectedUsn live off the current list-query data at click time, not from a
    // value captured when the row/dialog first rendered — same discipline as
    // DeleteDeckDialog.tsx's `deck.usn`, just sourced via `.find()` since this dialog
    // doesn't hold a per-row prop the way DeleteDeckDialog holds a single `deck` prop.
    const current = items.find((item) => item.id === id)
    if (!current?.id) {
      return
    }
    // current.usn: always populated for a persisted filtered deck; see the invariant
    // note above CardDetail.tsx's handleSave().
    deleteFilteredDeck.mutate(
      { id: current.id, params: { expectedUsn: current.usn } },
      {
        onSuccess: () => {
          void queryClient.invalidateQueries({
            queryKey: getFilteredDeckServiceListFilteredDecksQueryKey(),
          })
          toast({ title: t('filteredDecks.deleteSuccess'), tone: 'success' })
          setConfirmingDeleteId(null)
        },
        onError: () => {
          setDeleteError(t('error'))
        },
      },
    )
  }

  const isCreateDisabled =
    name.trim().length === 0 || query.trim().length === 0 || createFilteredDeck.isPending

  return (
    <Dialog open={open} onClose={handleClose} title={t('filteredDecks.dialogTitle')}>
      <div className="flex flex-col gap-6">
        <form onSubmit={handleCreate} className="flex flex-col gap-4">
          <Field label={t('filteredDecks.nameLabel')}>
            {(controlProps) => (
              <TextInput
                {...controlProps}
                value={name}
                onChange={(event) => setName(event.target.value)}
                placeholder={t('filteredDecks.namePlaceholder')}
              />
            )}
          </Field>

          <Field label={t('filteredDecks.queryLabel')}>
            {(controlProps) => (
              <TextInput
                {...controlProps}
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder={t('filteredDecks.queryPlaceholder')}
              />
            )}
          </Field>

          {createError ? (
            <p role="alert" className="text-sm text-danger">
              {createError}
            </p>
          ) : null}

          <Button type="submit" disabled={isCreateDisabled} loading={createFilteredDeck.isPending}>
            {t('filteredDecks.create')}
          </Button>
        </form>

        {listQuery.isPending ? (
          <div className="flex flex-col gap-2" aria-busy="true">
            <Spinner label={t('filteredDecks.loading')} />
            <Skeleton className="h-12 w-full" />
            <Skeleton className="h-12 w-full" />
          </div>
        ) : listQuery.isError ? (
          <p role="alert" className="text-sm text-danger">
            {t('error')}
          </p>
        ) : items.length === 0 ? (
          <EmptyState
            title={t('filteredDecks.empty')}
            description={t('filteredDecks.emptyDescription')}
          />
        ) : (
          <ul className="flex flex-col gap-2">
            {items.map((filteredDeck, index) => {
              const id = filteredDeck.id
              const isConfirming = id !== undefined && id === confirmingDeleteId
              return (
                <li
                  key={id ?? index}
                  className="flex flex-col gap-2 rounded-[var(--radius-md)] border border-border bg-surface p-3"
                >
                  <div className="flex items-center justify-between gap-3">
                    <div className="min-w-0">
                      <p className="truncate text-sm font-medium text-fg">{filteredDeck.name}</p>
                      <p className="truncate text-sm text-muted">{filteredDeck.query}</p>
                    </div>
                    {!isConfirming && id ? (
                      <Button
                        variant="danger"
                        size="sm"
                        onClick={() => {
                          setDeleteError(null)
                          setConfirmingDeleteId(id)
                        }}
                      >
                        {t('filteredDecks.delete')}
                      </Button>
                    ) : null}
                  </div>

                  {isConfirming ? (
                    <div className="flex flex-col gap-2">
                      <p className="text-sm text-fg">
                        {t('filteredDecks.deleteConfirm', { name: filteredDeck.name ?? '' })}
                      </p>
                      <div className="flex items-center justify-between gap-3">
                        <Button
                          variant="secondary"
                          size="sm"
                          onClick={() => setConfirmingDeleteId(null)}
                          disabled={deleteFilteredDeck.isPending}
                        >
                          {t('filteredDecks.deleteCancel')}
                        </Button>
                        <Button
                          variant="danger"
                          size="sm"
                          onClick={() => handleConfirmDelete(id)}
                          loading={deleteFilteredDeck.isPending}
                          disabled={deleteFilteredDeck.isPending}
                        >
                          {t('filteredDecks.deleteConfirmAction')}
                        </Button>
                      </div>
                    </div>
                  ) : null}
                </li>
              )
            })}
          </ul>
        )}

        {deleteError ? (
          <p role="alert" className="text-sm text-danger">
            {deleteError}
          </p>
        ) : null}
      </div>
    </Dialog>
  )
}
