import { useTranslation } from 'react-i18next'

import { useNoteTypeServiceListNoteTypes } from '../../lib/api/collection/note-type-service/note-type-service'
import type { V1ListNoteTypesResponse } from '../../lib/api/collection/collectionV1CollectionProto.schemas'
import { Dialog, EmptyState, Skeleton, Spinner } from '../../ui'

export interface NoteTypesDialogProps {
  open: boolean
  onClose: () => void
}

// Note-types browser (Task 11b, see task-11-brief.md). Read-only by design: the brief
// explicitly defers note-type creation/editing ("advanced note-type editing can be
// deferred (note it)") — this dialog only lists what already exists (name, field
// count, template count) so users can see what schemas are in play without this task
// taking on a full schema editor (fields/templates/CSS), which is a meaningfully
// larger surface than deck rename/delete/config from Task 11a.
//
// Separate dialog from FilteredDecksDialog (Constraint 1 — one screen/dialog = one
// action): browsing note types and managing filtered decks are unrelated tasks with
// no shared state, so combining them would mix two purposes behind one trigger.
export function NoteTypesDialog({ open, onClose }: NoteTypesDialogProps) {
  const { t } = useTranslation('decks')
  const { data, isPending, isError } = useNoteTypeServiceListNoteTypes(undefined, {
    query: { enabled: open },
  })

  // Same success-shape invariant as DecksScreen.tsx: customFetch throws on any
  // non-2xx response, so `data` is always the success shape once defined.
  const items = (data?.data as V1ListNoteTypesResponse | undefined)?.items ?? []

  return (
    <Dialog open={open} onClose={onClose} title={t('noteTypes.dialogTitle')}>
      {isPending ? (
        <div className="flex flex-col gap-2" aria-busy="true">
          <Spinner label={t('noteTypes.loading')} />
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-12 w-full" />
        </div>
      ) : isError ? (
        <p role="alert" className="text-sm text-danger">
          {t('error')}
        </p>
      ) : items.length === 0 ? (
        <EmptyState title={t('noteTypes.empty')} description={t('noteTypes.emptyDescription')} />
      ) : (
        <ul className="flex flex-col gap-2">
          {items.map((noteType, index) => {
            const fieldCount = noteType.fields?.length ?? 0
            const templateCount = noteType.cardTemplates?.length ?? 0
            return (
              <li
                key={noteType.id ?? index}
                className="rounded-[var(--radius-md)] border border-border bg-surface p-3"
              >
                <p className="text-sm font-medium text-fg">{noteType.name}</p>
                <p className="text-sm text-muted">
                  {t('noteTypes.fieldCount', { count: fieldCount })}
                  {' · '}
                  {t('noteTypes.templateCount', { count: templateCount })}
                </p>
              </li>
            )
          })}
        </ul>
      )}
    </Dialog>
  )
}
