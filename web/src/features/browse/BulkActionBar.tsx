import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useQueryClient } from '@tanstack/react-query'

import { useCardServiceBulkCards } from '../../lib/api/collection/card-service/card-service'
import { useDeckServiceListDecks } from '../../lib/api/collection/deck-service/deck-service'
import { V1BulkOp } from '../../lib/api/collection/collectionV1CollectionProto.schemas'
import type {
  V1BulkArgs,
  V1BulkCardsResponse,
  V1ListDecksResponse,
} from '../../lib/api/collection/collectionV1CollectionProto.schemas'
import { Button, Dialog, Field, Select, Spinner, TextInput, cn, toast } from '../../ui'

// Bulk action bar for the card browser (Browse bulk ops). Rendered by BrowseScreen when one
// or more result rows are selected; applies an operation to exactly the checked card IDs via
// CardService.BulkCards. The `idsList` selector (not the `query` selector) is used deliberately
// so an action only ever touches the cards the user can see and picked — the query selector
// would silently re-target unloaded pages of the current search.

// Colour flags mirror Anki's classic four (1-4); 0 clears the flag. The swatch colours are
// literal hex (not design tokens) because they encode fixed flag identities, not UI state.
const FLAG_OPTIONS = [
  { value: 0, key: 'clear' as const, color: null },
  { value: 1, key: 'red' as const, color: '#e5534b' },
  { value: 2, key: 'orange' as const, color: '#d98a3d' },
  { value: 3, key: 'green' as const, color: '#3fa45b' },
  { value: 4, key: 'blue' as const, color: '#3b7bd8' },
]

// Which ops open a dialog for their argument(s). Suspend takes no args and is applied directly.
type DialogOp = 'move' | 'tag' | 'untag' | 'flag' | 'delete' | null

export interface BulkActionBarProps {
  /** The card IDs the action applies to (the currently checked rows). */
  selectedIds: string[]
  /** Called after a successful bulk action so the caller can clear its selection. */
  onDone: () => void
}

export function BulkActionBar({ selectedIds, onDone }: BulkActionBarProps) {
  const { t } = useTranslation('browse')
  const queryClient = useQueryClient()
  const bulk = useCardServiceBulkCards()

  const [activeOp, setActiveOp] = useState<DialogOp>(null)
  const [deckId, setDeckId] = useState('')
  const [tagValue, setTagValue] = useState('')

  // Deck list for the Move picker. Loaded lazily only while the Move dialog is open; a single
  // (default) page is sufficient for typical deck counts — deep pagination is a future concern.
  const decksQuery = useDeckServiceListDecks(undefined, {
    query: { enabled: activeOp === 'move' },
  })
  const decks = (decksQuery.data?.data as V1ListDecksResponse | undefined)?.items ?? []

  function closeDialog() {
    setActiveOp(null)
    setDeckId('')
    setTagValue('')
  }

  function runBulk(op: V1BulkOp, args?: V1BulkArgs) {
    bulk.mutate(
      { data: { idsList: { ids: selectedIds }, op, args } },
      {
        onSuccess: (res) => {
          const affected = Number(
            (res.data as V1BulkCardsResponse | undefined)?.affectedCount ?? selectedIds.length,
          )
          toast({ title: t('bulk.success', { count: affected }), tone: 'success' })
          // Prefix-invalidate every active search variant (query/cursor/limit) so mutated or
          // removed rows refresh — same approach CardDetail uses after editing a note.
          void queryClient.invalidateQueries({ queryKey: ['/v1/search'] })
          closeDialog()
          onDone()
        },
        onError: () => {
          toast({ title: t('bulk.error'), tone: 'danger' })
        },
      },
    )
  }

  const isBusy = bulk.isPending

  return (
    <div className="flex flex-wrap items-center gap-2 rounded-[var(--radius-md)] border border-border bg-surface p-2">
      <span className="px-1 text-sm text-muted">
        {t('bulk.selected', { count: selectedIds.length })}
      </span>
      <div className="flex flex-wrap gap-2">
        <Button variant="secondary" onClick={() => setActiveOp('move')} disabled={isBusy}>
          {t('bulk.actions.move')}
        </Button>
        <Button variant="secondary" onClick={() => setActiveOp('tag')} disabled={isBusy}>
          {t('bulk.actions.tag')}
        </Button>
        <Button variant="secondary" onClick={() => setActiveOp('untag')} disabled={isBusy}>
          {t('bulk.actions.untag')}
        </Button>
        <Button variant="secondary" onClick={() => setActiveOp('flag')} disabled={isBusy}>
          {t('bulk.actions.flag')}
        </Button>
        <Button
          variant="secondary"
          onClick={() => runBulk(V1BulkOp.BULK_OP_SUSPEND)}
          loading={isBusy && activeOp === null}
          disabled={isBusy}
        >
          {t('bulk.actions.suspend')}
        </Button>
        <Button variant="danger" onClick={() => setActiveOp('delete')} disabled={isBusy}>
          {t('bulk.actions.delete')}
        </Button>
      </div>

      {/* Move */}
      <Dialog open={activeOp === 'move'} onClose={closeDialog} title={t('bulk.move.title')}>
        {decksQuery.isPending ? (
          <Spinner label={t('loading')} />
        ) : decks.length === 0 ? (
          <p className="text-sm text-muted">{t('bulk.move.noDecks')}</p>
        ) : (
          <div className="flex flex-col gap-4">
            <Field label={t('bulk.move.deckLabel')}>
              {(controlProps) => (
                <Select
                  {...controlProps}
                  value={deckId}
                  onChange={(e) => setDeckId(e.target.value)}
                >
                  <option value="" disabled>
                    {t('bulk.move.deckLabel')}
                  </option>
                  {decks.map((deck) => (
                    <option key={deck.id} value={deck.id}>
                      {deck.path ?? deck.name}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
            <div className="flex items-center justify-end gap-3">
              <Button variant="secondary" onClick={closeDialog} disabled={isBusy}>
                {t('bulk.cancel')}
              </Button>
              <Button
                onClick={() => runBulk(V1BulkOp.BULK_OP_MOVE, { targetDeckId: deckId })}
                loading={isBusy}
                disabled={isBusy || !deckId}
              >
                {t('bulk.move.confirm')}
              </Button>
            </div>
          </div>
        )}
      </Dialog>

      {/* Tag / Untag share a form; the op differs. */}
      <Dialog
        open={activeOp === 'tag' || activeOp === 'untag'}
        onClose={closeDialog}
        title={activeOp === 'untag' ? t('bulk.untag.title') : t('bulk.tag.title')}
      >
        <form
          onSubmit={(event) => {
            event.preventDefault()
            const tag = tagValue.trim()
            if (!tag) return
            runBulk(activeOp === 'untag' ? V1BulkOp.BULK_OP_UNTAG : V1BulkOp.BULK_OP_TAG, { tag })
          }}
          className="flex flex-col gap-4"
        >
          <Field label={t('bulk.tag.label')}>
            {(controlProps) => (
              <TextInput
                {...controlProps}
                value={tagValue}
                placeholder={t('bulk.tag.placeholder')}
                onChange={(e) => setTagValue(e.target.value)}
              />
            )}
          </Field>
          <div className="flex items-center justify-end gap-3">
            <Button type="button" variant="secondary" onClick={closeDialog} disabled={isBusy}>
              {t('bulk.cancel')}
            </Button>
            <Button type="submit" loading={isBusy} disabled={isBusy || !tagValue.trim()}>
              {activeOp === 'untag' ? t('bulk.untag.confirm') : t('bulk.tag.confirm')}
            </Button>
          </div>
        </form>
      </Dialog>

      {/* Flag */}
      <Dialog open={activeOp === 'flag'} onClose={closeDialog} title={t('bulk.flag.title')}>
        <div className="flex flex-wrap gap-2">
          {FLAG_OPTIONS.map((flag) => (
            <button
              key={flag.value}
              type="button"
              disabled={isBusy}
              onClick={() => runBulk(V1BulkOp.BULK_OP_FLAG, { flag: flag.value })}
              className="flex items-center gap-2 rounded-[var(--radius-md)] border border-border bg-surface px-3 py-2 text-sm text-fg transition-colors duration-[var(--duration-fast)] ease-default hover:bg-accent-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent disabled:opacity-60"
            >
              <span
                aria-hidden="true"
                className={cn('h-3 w-3 rounded-full', flag.color ? '' : 'border border-border')}
                style={flag.color ? { backgroundColor: flag.color } : undefined}
              />
              {t(`bulk.flag.${flag.key}`)}
            </button>
          ))}
        </div>
      </Dialog>

      {/* Delete (destructive, irreversible) */}
      <Dialog open={activeOp === 'delete'} onClose={closeDialog} title={t('bulk.delete.title')}>
        <div className="flex flex-col gap-4">
          <p className="text-sm text-fg">
            {t('bulk.delete.confirmText', { count: selectedIds.length })}
          </p>
          <div className="flex items-center justify-end gap-3">
            <Button variant="secondary" onClick={closeDialog} disabled={isBusy}>
              {t('bulk.cancel')}
            </Button>
            <Button
              variant="danger"
              onClick={() => runBulk(V1BulkOp.BULK_OP_DELETE)}
              loading={isBusy}
              disabled={isBusy}
            >
              {t('bulk.delete.confirm')}
            </Button>
          </div>
        </div>
      </Dialog>
    </div>
  )
}
