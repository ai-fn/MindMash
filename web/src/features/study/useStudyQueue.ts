import { useEffect, useRef, useState } from 'react'

import { useStudyServiceGetQueue } from '../../lib/api/scheduler/study-service/study-service'
import type {
  V1GetQueueResponse,
  V1QueueCard,
} from '../../lib/api/scheduler/schedulerV1SchedulerProto.schemas'
import {
  LOADING,
  advance,
  initMachine,
  reveal,
  type StudyCard,
  type StudyMachineState,
} from './studyMachine.ts'

function toStudyCards(cards: V1QueueCard[] | undefined): StudyCard[] {
  if (!cards) {
    return []
  }
  // Defensive filter, same convention as DecksScreen.tsx's deck.id guard: a queue
  // entry missing cardId/frontHtml can't be rendered or graded, so drop it rather
  // than risk a broken card / undefined-id mutation call.
  return cards
    .filter(
      (card): card is V1QueueCard & { cardId: string; frontHtml: string } =>
        Boolean(card.cardId) && card.frontHtml !== undefined,
    )
    .map((card) => ({
      cardId: card.cardId,
      deckId: card.deckId ?? '',
      frontHtml: card.frontHtml,
    }))
}

export type UseStudyQueueResult = {
  state: StudyMachineState
  currentCard: StudyCard | null
  reveal: () => void
  advanceAfterGrade: () => void
}

/**
 * Combines the queue fetch with Task 6a's pure `studyMachine`. The machine is
 * initialized exactly once per successful fetch for a given `deckId` — re-renders
 * or React Query background refetches never reset progress through the queue the
 * user is actively studying.
 */
export function useStudyQueue(deckId: string): UseStudyQueueResult {
  const { data, isPending } = useStudyServiceGetQueue(deckId)

  const [state, setState] = useState<StudyMachineState>(LOADING)
  // Tracks which deckId the machine currently in `state` was initialized for, so a
  // navigation to a different deck (same component instance) re-initializes rather
  // than keeps showing the previous deck's queue.
  const initializedForDeckId = useRef<string | null>(null)

  useEffect(() => {
    if (isPending) {
      return
    }
    if (initializedForDeckId.current === deckId) {
      return
    }
    const cards = toStudyCards((data?.data as V1GetQueueResponse | undefined)?.cards)
    initializedForDeckId.current = deckId
    setState(initMachine(cards))
  }, [deckId, isPending, data])

  // deckId changed: reset so the effect above re-initializes against the new fetch
  // instead of keeping the previous deck's machine state.
  useEffect(() => {
    if (initializedForDeckId.current !== null && initializedForDeckId.current !== deckId) {
      initializedForDeckId.current = null
      setState(LOADING)
    }
  }, [deckId])

  const queue =
    state.status === 'showingFront' || state.status === 'showingBack' ? state.queue : null
  const cursor =
    state.status === 'showingFront' || state.status === 'showingBack' ? state.cursor : null
  const currentCard = queue !== null && cursor !== null ? (queue[cursor] ?? null) : null

  return {
    state,
    currentCard,
    reveal: () => setState((current) => reveal(current)),
    advanceAfterGrade: () => setState((current) => advance(current)),
  }
}
