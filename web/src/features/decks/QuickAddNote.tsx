import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'

import { useNoteServiceCreateNote } from '../../lib/api/collection/note-service/note-service'
import { useBasicNoteType } from './useBasicNoteType'

export type QuickAddNoteProps = {
  deckId: string
}

// Minimal inline quick-add-note form (Task 7, see task-7-brief.md). Plain
// Front/Back text only — no cloze/media/LaTeX/rich-text, this is explicitly the
// minimum authoring surface needed to reach a studyable state, not a real editor.
// Transparently ensures the "Basic" note type exists via `useBasicNoteType`.
export function QuickAddNote({ deckId }: QuickAddNoteProps) {
  const { t } = useTranslation('authoring')
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
    <form
      onSubmit={handleSubmit}
      className="flex flex-col gap-2 rounded border border-slate-200 bg-slate-50 p-3"
    >
      {isNoteTypePending && <p className="text-sm text-slate-600">{t('quickAddNote.preparing')}</p>}
      <label className="flex flex-col gap-1 text-sm text-slate-700">
        {t('quickAddNote.frontLabel')}
        <input
          type="text"
          value={front}
          onChange={(event) => setFront(event.target.value)}
          className="rounded border border-slate-300 px-3 py-2"
        />
      </label>
      <label className="flex flex-col gap-1 text-sm text-slate-700">
        {t('quickAddNote.backLabel')}
        <input
          type="text"
          value={back}
          onChange={(event) => setBack(event.target.value)}
          className="rounded border border-slate-300 px-3 py-2"
        />
      </label>
      <button
        type="submit"
        disabled={isDisabled}
        className="self-start rounded bg-slate-800 px-4 py-2 text-white disabled:opacity-50"
      >
        {t('quickAddNote.submit')}
      </button>
      {error && <p className="text-sm text-red-600">{error}</p>}
    </form>
  )
}
