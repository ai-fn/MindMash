import { useTranslation } from 'react-i18next'
import { useQueryClient } from '@tanstack/react-query'

import {
  getNoteTypeServiceListNoteTypesQueryKey,
  useNoteTypeServiceListNoteTypes,
  useNoteTypeServiceUpdateNoteType,
} from '../../lib/api/collection/note-type-service/note-type-service'
import type {
  V1ListNoteTypesResponse,
  V1NoteType,
} from '../../lib/api/collection/collectionV1CollectionProto.schemas'
import { Dialog, EmptyState, Field, Select, Skeleton, Spinner, toast } from '../../ui'

export interface NoteTypesDialogProps {
  open: boolean
  onClose: () => void
}

// Search-analyzer languages selectable per field (i18n spec 13 step 3). The empty
// value is language-agnostic (standard analyzer only); en/ru/zh route the field's
// text to the matching per-language analyzer in Search. Any other value is treated
// as agnostic server-side, so the list is deliberately limited to the app's scope.
const SEARCH_LANGUAGES = ['', 'en', 'ru', 'zh'] as const

// Note-types browser (Task 11b, see task-11-brief.md). Originally read-only; it now
// also lets users set each field's **search language** — the on-ramp for per-language
// search analyzers (spec 13 step 3), whose backend is otherwise unreachable from the
// UI. Full schema editing (add/rename/remove fields, templates, CSS) stays deferred:
// this only edits the one `language` attribute of existing fields, keeping the surface
// small while making the analyzer feature usable.
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
          {items.map((noteType, index) => (
            <NoteTypeItem key={noteType.id ?? index} noteType={noteType} />
          ))}
        </ul>
      )}
    </Dialog>
  )
}

// NoteTypeItem renders one note type and owns its update mutation (a component per
// row keeps the mutation hook out of a loop, satisfying the rules of hooks). The
// language selects are driven by server data, so an invalidated refetch reflects the
// saved value; they are disabled while this note type's write is in flight.
function NoteTypeItem({ noteType }: { noteType: V1NoteType }) {
  const { t } = useTranslation('decks')
  const queryClient = useQueryClient()
  const updateNoteType = useNoteTypeServiceUpdateNoteType()

  const fields = noteType.fields ?? []
  const fieldCount = fields.length
  const templateCount = noteType.cardTemplates?.length ?? 0

  function handleLanguageChange(fieldName: string, language: string) {
    if (!noteType.id) {
      return
    }
    // UpdateNoteType replaces the full desired state, so send every field back with
    // its name/sticky/rtl preserved and only the target field's language changed —
    // sending `{ language }` alone would clear the other attributes. expectedUsn is
    // read fresh from the server object for optimistic-concurrency (see RenameDeck).
    const nextFields = fields.map((f) => (f.name === fieldName ? { ...f, language } : f))
    updateNoteType.mutate(
      {
        id: noteType.id,
        data: {
          name: noteType.name,
          fields: nextFields,
          cardTemplates: noteType.cardTemplates,
          css: noteType.css,
          expectedUsn: noteType.usn,
        },
      },
      {
        onSuccess: () => {
          void queryClient.invalidateQueries({
            queryKey: getNoteTypeServiceListNoteTypesQueryKey(),
          })
          toast({ title: t('noteTypes.languageSaved'), tone: 'success' })
        },
        onError: () => {
          toast({ title: t('error'), tone: 'danger' })
        },
      },
    )
  }

  return (
    <li className="rounded-[var(--radius-md)] border border-border bg-surface p-3">
      <p className="text-sm font-medium text-fg">{noteType.name}</p>
      <p className="text-sm text-muted">
        {t('noteTypes.fieldCount', { count: fieldCount })}
        {' · '}
        {t('noteTypes.templateCount', { count: templateCount })}
      </p>
      {fieldCount > 0 ? (
        <div className="mt-3 flex flex-col gap-2">
          <p className="text-xs font-medium text-muted">{t('noteTypes.fieldLanguageHeading')}</p>
          {fields.map((field, index) => (
            <Field key={field.name ?? index} label={field.name ?? ''}>
              {(controlProps) => (
                <Select
                  {...controlProps}
                  value={field.language ?? ''}
                  disabled={updateNoteType.isPending}
                  onChange={(event) => handleLanguageChange(field.name ?? '', event.target.value)}
                >
                  {SEARCH_LANGUAGES.map((code) => (
                    <option key={code || 'agnostic'} value={code}>
                      {t(`noteTypes.language.${code || 'agnostic'}`)}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
          ))}
        </div>
      ) : null}
    </li>
  )
}
