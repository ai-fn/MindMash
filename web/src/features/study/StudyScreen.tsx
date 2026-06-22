import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useParams } from 'react-router-dom'

import { useCardServiceGetCard } from '../../lib/api/collection/card-service/card-service'
import type { V1GetCardResponse } from '../../lib/api/collection/collectionV1CollectionProto.schemas'
import {
  useStudyServiceAnswer,
  useStudyServiceGetPreview,
} from '../../lib/api/scheduler/study-service/study-service'
import type {
  V1GetPreviewResponse,
  V1Grade,
} from '../../lib/api/scheduler/schedulerV1SchedulerProto.schemas'
import { V1Grade as Grade } from '../../lib/api/scheduler/schedulerV1SchedulerProto.schemas'
import { CardFrame } from './CardFrame.tsx'
import { useStudyQueue } from './useStudyQueue.ts'

const GRADE_BUTTONS: { grade: V1Grade; labelKey: string; key: string }[] = [
  { grade: Grade.GRADE_AGAIN, labelKey: 'grade.again', key: '1' },
  { grade: Grade.GRADE_HARD, labelKey: 'grade.hard', key: '2' },
  { grade: Grade.GRADE_GOOD, labelKey: 'grade.good', key: '3' },
  { grade: Grade.GRADE_EASY, labelKey: 'grade.easy', key: '4' },
]

/**
 * Formats a seconds-as-string interval (V1IntervalPreview's wire format) into a
 * rough human-readable duration. Intentionally simple for MVP — no full i18n
 * duration library, see task-6b-brief.md.
 */
function formatIntervalSeconds(seconds: string | undefined): string | null {
  if (seconds === undefined) {
    return null
  }
  const n = Number(seconds)
  if (!Number.isFinite(n) || n < 0) {
    return null
  }
  if (n < 60) {
    return `${Math.round(n)}s`
  }
  const minutes = n / 60
  if (minutes < 60) {
    return `${Math.round(minutes)}m`
  }
  const hours = minutes / 60
  if (hours < 24) {
    return `${Math.round(hours)}h`
  }
  const days = hours / 24
  return `${Math.round(days)}d`
}

function previewLabelFor(preview: V1GetPreviewResponse | undefined, grade: V1Grade): string | null {
  if (!preview?.preview) {
    return null
  }
  const { againSec, hardSec, goodSec, easySec } = preview.preview
  switch (grade) {
    case Grade.GRADE_AGAIN:
      return formatIntervalSeconds(againSec)
    case Grade.GRADE_HARD:
      return formatIntervalSeconds(hardSec)
    case Grade.GRADE_GOOD:
      return formatIntervalSeconds(goodSec)
    case Grade.GRADE_EASY:
      return formatIntervalSeconds(easySec)
    default:
      return null
  }
}

// Route component for /study/:deckId (Task 6b). Wires Task 6a's pure studyMachine
// (via useStudyQueue) to the real scheduler/collection APIs and renders Task 5's
// sandboxed CardFrame for card content.
export function StudyScreen() {
  const { t } = useTranslation('study')
  const { deckId } = useParams<{ deckId: string }>()
  const { state, currentCard, reveal, advanceAfterGrade } = useStudyQueue(deckId ?? '')
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [answerError, setAnswerError] = useState(false)

  const isShowingFront = state.status === 'showingFront'
  const isShowingBack = state.status === 'showingBack'

  // Prefetch the current front card's back content as soon as it becomes the
  // current showingFront card (a simpler, equally valid interpretation of "prefetch
  // the next card" — see task-6b-brief.md judgment call: fetching card N's back
  // content while card N is still the front card, not literally card N+1) — so the
  // back is ready with zero extra latency once the user reveals.
  const cardQuery = useCardServiceGetCard(currentCard?.cardId ?? '', {
    query: { enabled: Boolean(currentCard?.cardId) },
  })
  const cardData = cardQuery.data?.data as V1GetCardResponse | undefined

  const previewQuery = useStudyServiceGetPreview(currentCard?.cardId ?? '', {
    query: { enabled: isShowingBack && Boolean(currentCard?.cardId) },
  })
  const previewData = previewQuery.data?.data as V1GetPreviewResponse | undefined

  const answerMutation = useStudyServiceAnswer()

  function submitGrade(grade: V1Grade) {
    if (isSubmitting || !currentCard) {
      return
    }
    setIsSubmitting(true)
    setAnswerError(false)
    answerMutation.mutate(
      { cardId: currentCard.cardId, data: { grade } },
      {
        onSuccess: () => {
          setIsSubmitting(false)
          advanceAfterGrade()
        },
        onError: () => {
          setIsSubmitting(false)
          setAnswerError(true)
        },
      },
    )
  }

  useEffect(() => {
    function handleKeyDown(event: KeyboardEvent) {
      if (isSubmitting) {
        return
      }
      if (isShowingFront && event.code === 'Space') {
        event.preventDefault()
        reveal()
        return
      }
      if (isShowingBack) {
        const button = GRADE_BUTTONS.find((b) => b.key === event.key)
        if (button) {
          event.preventDefault()
          submitGrade(button.grade)
        }
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isShowingFront, isShowingBack, isSubmitting, currentCard])

  if (state.status === 'loading') {
    return (
      <main className="flex min-h-screen items-center justify-center bg-slate-50">
        <p className="text-slate-600">{t('loading')}</p>
      </main>
    )
  }

  if (state.status === 'empty') {
    return (
      <main className="flex min-h-screen flex-col items-center justify-center gap-4 bg-slate-50">
        <p className="text-slate-600">{t('empty')}</p>
        <Link to="/" className="text-blue-600 underline">
          {t('backToDecks')}
        </Link>
      </main>
    )
  }

  if (state.status === 'done') {
    return (
      <main className="flex min-h-screen flex-col items-center justify-center gap-4 bg-slate-50">
        <p className="text-slate-600">{t('deckComplete')}</p>
        <Link to="/" className="text-blue-600 underline">
          {t('backToDecks')}
        </Link>
      </main>
    )
  }

  if (!currentCard) {
    // Defensive: studyMachine guarantees showingFront/showingBack always carry a
    // valid cursor into a non-empty queue, so this should be unreachable — but
    // satisfies the type checker without an assertion.
    return null
  }

  return (
    <main className="flex min-h-screen flex-col gap-6 bg-slate-50 p-6">
      {isShowingFront ? (
        <>
          <CardFrame html={currentCard.frontHtml} onReveal={reveal} />
          <button
            type="button"
            onClick={reveal}
            className="rounded bg-slate-800 px-4 py-2 text-white"
          >
            {t('showAnswer')}
          </button>
        </>
      ) : (
        <>
          <CardFrame html={currentCard.frontHtml + (cardData?.backHtml ?? '')} />
          {answerError ? <p className="text-red-600">{t('answerError')}</p> : null}
          <div className="flex gap-2">
            {GRADE_BUTTONS.map(({ grade, labelKey }) => {
              const previewLabel = previewLabelFor(previewData, grade)
              return (
                <button
                  key={grade}
                  type="button"
                  disabled={isSubmitting}
                  onClick={() => submitGrade(grade)}
                  className="flex flex-col items-center rounded bg-slate-800 px-4 py-2 text-white disabled:opacity-50"
                >
                  <span>{t(labelKey)}</span>
                  {previewLabel ? (
                    <span className="text-xs text-slate-300">{previewLabel}</span>
                  ) : null}
                </button>
              )
            })}
          </div>
        </>
      )}
    </main>
  )
}
