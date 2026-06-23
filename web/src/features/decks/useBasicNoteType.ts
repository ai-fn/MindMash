import { useEffect, useRef } from 'react'

import {
  useNoteTypeServiceCreateNoteType,
  useNoteTypeServiceListNoteTypes,
} from '../../lib/api/collection/note-type-service/note-type-service'
import type {
  V1CreateNoteTypeResponse,
  V1ListNoteTypesResponse,
} from '../../lib/api/collection/collectionV1CollectionProto.schemas'

const BASIC_NOTE_TYPE_NAME = 'Basic'

// Mirrors Anki's own built-in "Basic" note type templates exactly — see
// task-7-brief.md: `{{FrontSide}}` is a backend-supported special reference to the
// rendered front, per Anki template convention.
const BASIC_NOTE_TYPE_REQUEST = {
  name: BASIC_NOTE_TYPE_NAME,
  fields: [{ name: 'Front' }, { name: 'Back' }],
  cardTemplates: [
    {
      name: 'Card 1',
      front: '{{Front}}',
      back: '{{FrontSide}}<hr>{{Back}}',
    },
  ],
}

export type UseBasicNoteTypeResult = {
  noteTypeId: string | null
  isPending: boolean
}

/**
 * Ensure-or-create the "Basic" note type used by `QuickAddNote` (Task 7, see
 * task-7-brief.md). A fresh user/org has no note types yet, so this transparently
 * creates one — matching Anki's own built-in "Basic" note type — the first time
 * it's needed, and reuses it on every subsequent call.
 */
export function useBasicNoteType(): UseBasicNoteTypeResult {
  const { data, isPending: isListPending } = useNoteTypeServiceListNoteTypes()
  const createNoteType = useNoteTypeServiceCreateNoteType()

  // Guards the create call so it only fires once even across re-renders / React
  // StrictMode's double-invoke of effects — without this guard a fresh user could
  // end up with multiple "Basic" note types racing each other.
  const hasRequestedCreate = useRef(false)

  const items = (data?.data as V1ListNoteTypesResponse | undefined)?.items
  const existing = items?.find((noteType) => noteType.name === BASIC_NOTE_TYPE_NAME)

  useEffect(() => {
    if (isListPending || existing || hasRequestedCreate.current) {
      return
    }
    hasRequestedCreate.current = true
    createNoteType.mutate({ data: BASIC_NOTE_TYPE_REQUEST })
    // createNoteType is a stable mutation object reference per Task 6's established
    // mutation-hook convention (see StudyScreen.test.tsx); only re-run this effect
    // when the inputs that determine whether we still need to create change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isListPending, existing])

  const createdNoteType = (createNoteType.data?.data as V1CreateNoteTypeResponse | undefined)
    ?.noteType

  const noteTypeId = existing?.id ?? createdNoteType?.id ?? null
  const isPending = isListPending || (!existing && !createdNoteType)

  return { noteTypeId, isPending }
}
