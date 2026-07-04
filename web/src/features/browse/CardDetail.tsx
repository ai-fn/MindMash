import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useQueryClient } from '@tanstack/react-query'

import { useCardServiceGetCard } from '../../lib/api/collection/card-service/card-service'
import {
  getNoteServiceGetNoteQueryKey,
  useNoteServiceDeleteNote,
  useNoteServiceGetNote,
  useNoteServiceUpdateNote,
} from '../../lib/api/collection/note-service/note-service'
import type {
  V1Card,
  V1GetCardResponse,
  V1GetNoteResponse,
} from '../../lib/api/collection/collectionV1CollectionProto.schemas'
import { CardFrame } from '../study/CardFrame.tsx'
import { Button, Dialog, Field, Skeleton, Spinner, Textarea, TextInput, toast } from '../../ui'

export interface CardDetailProps {
  /** The card to load and edit. `null` renders nothing (closed Dialog). */
  cardId: string | null
  onClose: () => void
}

// Card detail & editor (Task 10, see task-10-brief.md). Opened from BrowseScreen's result
// rows (and reusable from any other future call site — it only needs a `cardId`). Loads
// the card first (for the live preview + the noteId link), then the note (for the editable
// fields map), and lets the user edit fields or delete the underlying note.
//
// Single-line vs multi-line field heuristic: a field renders as a Textarea if its current
// value contains a newline OR exceeds FIELD_TEXTAREA_THRESHOLD characters, otherwise a
// single-line TextInput. Most Anki-style fields (front/back of a basic card) are short
// phrases that fit comfortably in a single line; some (extended answers, cloze passages)
// are paragraph-length. There's no schema-level "multiline" flag on V1NoteFields (it's a
// plain string map), so this is a content-based heuristic rather than a real field-type
// signal — documented here rather than guessed at without a comment.
const FIELD_TEXTAREA_THRESHOLD = 80

function isLongField(value: string): boolean {
  return value.includes('\n') || value.length > FIELD_TEXTAREA_THRESHOLD
}

export function CardDetail({ cardId, onClose }: CardDetailProps) {
  const { t: tAuthoring } = useTranslation('authoring')
  const { t: tBrowse } = useTranslation('browse')
  const queryClient = useQueryClient()

  // Local edit buffer: fields are seeded from the loaded note once available (see the
  // render-time seeding below) and then track user edits until save/close.
  const [fields, setFields] = useState<Record<string, string> | null>(null)
  const [seededForNoteId, setSeededForNoteId] = useState<string | null>(null)
  const [confirmingDelete, setConfirmingDelete] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const cardQuery = useCardServiceGetCard(cardId ?? '', { query: { enabled: cardId !== null } })
  const cardResponse = cardQuery.data?.data as V1GetCardResponse | undefined
  const card = cardResponse?.card as V1Card | undefined
  const noteId = card?.noteId

  const noteQuery = useNoteServiceGetNote(noteId ?? '', { query: { enabled: !!noteId } })
  const note = (noteQuery.data?.data as V1GetNoteResponse | undefined)?.note

  // Seed (or re-seed, if a different note loads) the edit buffer from the freshly loaded
  // note. Applied during render rather than in an effect, matching this codebase's existing
  // pattern (see BrowseScreen's pagination reset) for synchronous, render-time derivations
  // rather than a setState-in-effect cascade.
  if (note?.id && note.id !== seededForNoteId) {
    setFields({ ...(note.fields ?? {}) })
    setSeededForNoteId(note.id)
  }

  const updateNote = useNoteServiceUpdateNote()
  const deleteNote = useNoteServiceDeleteNote()

  const isLoading = cardQuery.isPending || (!!noteId && noteQuery.isPending)
  const isError = cardQuery.isError || noteQuery.isError

  function handleClose() {
    setFields(null)
    setSeededForNoteId(null)
    setConfirmingDelete(false)
    setError(null)
    onClose()
  }

  function invalidateAfterMutation() {
    if (noteId) {
      void queryClient.invalidateQueries({ queryKey: getNoteServiceGetNoteQueryKey(noteId) })
    }
    // No exported key factory is imported here for search (kept to the literal route
    // prefix) — confirmed search-service.ts does export getSearchServiceSearchQueryKey,
    // but it's parameterized per query/cursor/limit and BrowseScreen may have any of
    // those active. Invalidating by the shared URL prefix catches every variant.
    void queryClient.invalidateQueries({ queryKey: ['/v1/search'] })
  }

  function handleFieldChange(name: string, value: string) {
    setFields((prev) => ({ ...(prev ?? {}), [name]: value }))
  }

  // `note.usn` is typed `string | undefined` (every scalar field on the generated wire
  // types is optional — see V1Note in collectionV1CollectionProto.schemas.ts, same as
  // `id`), but is populated by the backend for every persisted resource exactly like
  // `id` is — there is no code path that returns a real note with an id but no usn.
  // Not additionally guarded here (and at the equivalent expectedUsn call sites in
  // RenameDeck.tsx/DeleteDeckDialog.tsx/DeckConfigDialog.tsx/FilteredDecksDialog.tsx):
  // an `undefined` expectedUsn would already surface as a mutation onError (the
  // backend's optimistic-concurrency check has nothing to compare against), which these
  // components already handle — a client-side guard would only replace that visible
  // error with a confusing silent no-op for a case that cannot occur in practice.
  function handleSave() {
    if (!note?.id || !fields) {
      return
    }
    setError(null)
    updateNote.mutate(
      {
        id: note.id,
        data: {
          fields,
          tags: note.tags,
          flags: note.flags,
          expectedUsn: note.usn,
        },
      },
      {
        onSuccess: () => {
          invalidateAfterMutation()
          toast({ title: tAuthoring('cardDetail.saveSuccess'), tone: 'success' })
        },
        onError: () => {
          setError(tAuthoring('error'))
        },
      },
    )
  }

  function handleConfirmDelete() {
    if (!note?.id) {
      return
    }
    setError(null)
    // note.usn: see the invariant note above handleSave().
    deleteNote.mutate(
      { id: note.id, params: { expectedUsn: note.usn } },
      {
        onSuccess: () => {
          invalidateAfterMutation()
          toast({ title: tAuthoring('cardDetail.deleteSuccess'), tone: 'success' })
          handleClose()
        },
        onError: () => {
          setError(tAuthoring('error'))
          setConfirmingDelete(false)
        },
      },
    )
  }

  const fieldEntries = Object.entries(fields ?? {})
  const isMutating = updateNote.isPending || deleteNote.isPending

  return (
    <Dialog
      open={cardId !== null}
      onClose={handleClose}
      title={tAuthoring('cardDetail.dialogTitle')}
    >
      {isLoading ? (
        <div className="flex flex-col gap-2" aria-busy="true">
          <Spinner label={tBrowse('loading')} />
          <Skeleton className="h-24 w-full" />
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-full" />
        </div>
      ) : isError ? (
        <p role="alert" className="text-sm text-danger">
          {tAuthoring('error')}
        </p>
      ) : !card || !note ? null : confirmingDelete ? (
        <div className="flex flex-col gap-4">
          <p className="text-sm text-fg">{tAuthoring('cardDetail.deleteConfirm')}</p>
          {error ? (
            <p role="alert" className="text-sm text-danger">
              {error}
            </p>
          ) : null}
          <div className="flex items-center justify-between gap-3">
            <Button
              variant="secondary"
              onClick={() => setConfirmingDelete(false)}
              disabled={deleteNote.isPending}
            >
              {tAuthoring('cardDetail.deleteCancel')}
            </Button>
            <Button
              variant="danger"
              onClick={handleConfirmDelete}
              loading={deleteNote.isPending}
              disabled={deleteNote.isPending}
            >
              {tAuthoring('cardDetail.deleteConfirmAction')}
            </Button>
          </div>
        </div>
      ) : (
        <div className="flex flex-col gap-5">
          <div className="overflow-hidden rounded-[var(--radius-md)] border border-border">
            <CardFrame html={cardResponse?.backHtml ?? cardResponse?.frontHtml ?? ''} />
          </div>

          <form
            onSubmit={(event) => {
              event.preventDefault()
              handleSave()
            }}
            className="flex flex-col gap-4"
          >
            {fieldEntries.map(([name, value]) => (
              <Field key={name} label={name}>
                {(controlProps) =>
                  isLongField(value) ? (
                    <Textarea
                      {...controlProps}
                      value={value}
                      onChange={(event) => handleFieldChange(name, event.target.value)}
                    />
                  ) : (
                    <TextInput
                      {...controlProps}
                      value={value}
                      onChange={(event) => handleFieldChange(name, event.target.value)}
                    />
                  )
                }
              </Field>
            ))}

            {error ? (
              <p role="alert" className="text-sm text-danger">
                {error}
              </p>
            ) : null}

            <div className="flex items-center justify-between gap-3">
              <Button
                type="button"
                variant="danger"
                onClick={() => setConfirmingDelete(true)}
                disabled={isMutating}
              >
                {tAuthoring('cardDetail.delete')}
              </Button>
              <Button type="submit" loading={updateNote.isPending} disabled={isMutating}>
                {tAuthoring('cardDetail.save')}
              </Button>
            </div>
          </form>
        </div>
      )}
    </Dialog>
  )
}
