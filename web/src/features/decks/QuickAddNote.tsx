import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { useQueryClient } from '@tanstack/react-query'

import { useNoteServiceCreateNote } from '../../lib/api/collection/note-service/note-service'
import { getStudyServiceGetCountsQueryKey } from '../../lib/api/scheduler/study-service/study-service'
import { Button, Card, Field, TextInput } from '../../ui'
import { useBasicNoteType } from './useBasicNoteType'

export type QuickAddNoteProps = {
  deckId: string
}

// Minimal inline quick-add-note form (Task 7, see task-7-brief.md), restyled (Task 5)
// onto the primitives. Plain Front/Back text only — no cloze/media/LaTeX/rich-text,
// this is explicitly the minimum authoring surface needed to reach a studyable
// state, not a real editor. Stays inline (not a Dialog) since it's already gated
// behind the per-row "Add card" toggle and only one deck's form is ever open at a
// time (enforced by DecksScreen's openDeckId state) — promoting it to a Dialog would
// add a second way to reach the same "one thing open at a time" outcome without
// adding value. Transparently ensures the "Basic" note type exists via
// `useBasicNoteType`.
export function QuickAddNote({ deckId }: QuickAddNoteProps) {
  const { t } = useTranslation('authoring')
  const queryClient = useQueryClient()
  const { noteTypeId, isPending: isNoteTypePending } = useBasicNoteType()
  const createNote = useNoteServiceCreateNote()
  const [front, setFront] = useState('')
  const [back, setBack] = useState('')
  const [error, setError] = useState<string | null>(null)

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!noteTypeId) {
      return
    }
    setError(null)
    createNote.mutate(
      { data: { noteTypeId, deckId, fields: { Front: front, Back: back } } },
      {
        onSuccess: () => {
          setFront('')
          setBack('')
          void queryClient.invalidateQueries({
            queryKey: getStudyServiceGetCountsQueryKey(deckId),
          })
        },
        onError: () => {
          setError(t('error'))
        },
      },
    )
  }

  const isDisabled =
    front.trim().length === 0 || back.trim().length === 0 || !noteTypeId || createNote.isPending

  return (
    <Card bordered className="flex flex-col gap-3 bg-bg">
      <form onSubmit={handleSubmit} className="flex flex-col gap-3">
        {isNoteTypePending ? <p className="text-sm text-muted">{t('quickAddNote.preparing')}</p> : null}

        <Field label={t('quickAddNote.frontLabel')}>
          {(controlProps) => (
            <TextInput {...controlProps} value={front} onChange={(event) => setFront(event.target.value)} />
          )}
        </Field>

        <Field label={t('quickAddNote.backLabel')}>
          {(controlProps) => (
            <TextInput {...controlProps} value={back} onChange={(event) => setBack(event.target.value)} />
          )}
        </Field>

        {error ? (
          <p role="alert" className="text-sm text-danger">
            {error}
          </p>
        ) : null}

        <Button type="submit" size="sm" disabled={isDisabled} loading={createNote.isPending} className="self-start">
          {t('quickAddNote.submit')}
        </Button>
      </form>
    </Card>
  )
}
