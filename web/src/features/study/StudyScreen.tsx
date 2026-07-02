import { useEffect, useMemo, useRef, useState } from 'react'
import type { PointerEvent as ReactPointerEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'

import { useCardServiceGetCard } from '../../lib/api/collection/card-service/card-service'
import type { V1GetCardResponse } from '../../lib/api/collection/collectionV1CollectionProto.schemas'
import { useDeckServiceGetDeck } from '../../lib/api/collection/deck-service/deck-service'
import type { V1GetDeckResponse } from '../../lib/api/collection/collectionV1CollectionProto.schemas'
import {
  getStudyServiceGetCountsQueryKey,
  useStudyServiceAnswer,
  useStudyServiceGetPreview,
} from '../../lib/api/scheduler/study-service/study-service'
import type {
  V1GetPreviewResponse,
  V1Grade,
} from '../../lib/api/scheduler/schedulerV1SchedulerProto.schemas'
import { V1Grade as Grade } from '../../lib/api/scheduler/schedulerV1SchedulerProto.schemas'
import { ArrowLeftIcon, Button, IconButton, toast } from '../../ui'
import { CardFrame } from './CardFrame.tsx'
import { useStudyPrefsStore } from './studyPrefsStore.ts'
import { useStudyQueue } from './useStudyQueue.ts'

const GRADE_BUTTONS: { grade: V1Grade; labelKey: string; key: string }[] = [
  { grade: Grade.GRADE_AGAIN, labelKey: 'grade.again', key: '1' },
  { grade: Grade.GRADE_HARD, labelKey: 'grade.hard', key: '2' },
  { grade: Grade.GRADE_GOOD, labelKey: 'grade.good', key: '3' },
  { grade: Grade.GRADE_EASY, labelKey: 'grade.easy', key: '4' },
]

// Minimum horizontal drag distance (px) before a pointer drag counts as a
// swipe-to-grade gesture rather than an incidental tap/click. Not a spec number — a
// reasonable UX default large enough to avoid firing on small jitter/clicks but small
// enough to feel responsive on a typical phone screen.
const SWIPE_THRESHOLD_PX = 80

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

type GradeFlash = 'know' | 'dontKnow' | null

// Route component for /study/:deckId (Task 6). Wires the pure studyMachine (via
// useStudyQueue) to the real scheduler/collection APIs and renders the sandboxed
// CardFrame for card content. Rebuilt to the UX brief's Global Constraints — see
// task-6-brief.md and task-6-report.md for the full set of judgment calls.
export function StudyScreen() {
  const { t } = useTranslation('study')
  const { deckId } = useParams<{ deckId: string }>()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { state, currentCard, reveal, advanceAfterGrade, undoLastGrade } = useStudyQueue(
    deckId ?? '',
  )
  const advancedGrading = useStudyPrefsStore((s) => s.advancedGrading)
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [answerError, setAnswerError] = useState(false)
  // Brief post-grade color flash on the card container (Constraint 6). Purely
  // visual/CSS-driven — cleared on a timer, never gates the actual advance()/mutation.
  const [gradeFlash, setGradeFlash] = useState<GradeFlash>(null)

  const isShowingFront = state.status === 'showingFront'
  const isShowingBack = state.status === 'showingBack'

  const deckQuery = useDeckServiceGetDeck(deckId ?? '', { query: { enabled: Boolean(deckId) } })
  const deckData = deckQuery.data?.data as V1GetDeckResponse | undefined

  // Progress `current / total` derived directly from the machine's queue/cursor — no
  // separate counter to keep in sync (Constraint 4's "12 / 30" context bar).
  const queueLength =
    state.status === 'showingFront' || state.status === 'showingBack' ? state.queue.length : 0
  const cursorPosition =
    state.status === 'showingFront' || state.status === 'showingBack' ? state.cursor + 1 : 0

  // Prefetch the current front card's back content as soon as it becomes the
  // current showingFront card — see task-6b-brief.md judgment call: fetching card N's
  // back content while card N is still the front card, not literally card N+1 — so
  // the back is ready with zero extra latency once the user reveals. Kept verbatim
  // from the prior implementation; this task must not regress it (brief item 8: no
  // loading state between cards).
  const cardQuery = useCardServiceGetCard(currentCard?.cardId ?? '', {
    query: { enabled: Boolean(currentCard?.cardId) },
  })
  const cardData = cardQuery.data?.data as V1GetCardResponse | undefined

  const previewQuery = useStudyServiceGetPreview(currentCard?.cardId ?? '', {
    query: { enabled: isShowingBack && Boolean(currentCard?.cardId) },
  })
  const previewData = previewQuery.data?.data as V1GetPreviewResponse | undefined

  const answerMutation = useStudyServiceAnswer()

  function flashFor(grade: V1Grade): GradeFlash {
    return grade === Grade.GRADE_AGAIN ? 'dontKnow' : 'know'
  }

  function submitGrade(grade: V1Grade) {
    if (isSubmitting || !currentCard) {
      return
    }
    setIsSubmitting(true)
    setAnswerError(false)
    setGradeFlash(flashFor(grade))
    answerMutation.mutate(
      { cardId: currentCard.cardId, data: { grade } },
      {
        onSuccess: () => {
          setIsSubmitting(false)
          // Whether this grade was the last card in the queue, computed from the
          // pre-advance state/cursor (same predicate studyMachine's own advance() uses
          // internally) — needed below to decide whether to offer an Undo action, since
          // advanceAfterGrade() below is async (setState) and state.status won't reflect
          // 'done' until the next render.
          const wasLastCard =
            state.status === 'showingBack' && state.cursor + 1 >= state.queue.length
          advanceAfterGrade()
          if (deckId) {
            void queryClient.invalidateQueries({
              queryKey: getStudyServiceGetCountsQueryKey(deckId),
            })
          }
          // Undo: there is no backend "un-answer" endpoint (confirmed — study-service.ts
          // only exposes Answer/GetPreview/Reschedule/GetQueue/GetCounts, nothing
          // resembling a revert). Undo therefore only rewinds the LOCAL session
          // queue/UI back to this card's showingBack state via undoLastGrade() — it
          // does NOT revert the FSRS state update the backend already persisted from
          // this grade. If the user re-grades the "undone" card differently, the
          // backend ends up with two recorded answers for it, not one corrected one.
          // Accepted MVP limitation per the brief's own instruction to document it.
          //
          // On the LAST card, advanceAfterGrade() above moves the machine to `done`
          // (see studyMachine.ts's rewindToShowingBack doc comment: undo is a no-op
          // from `done`, there being no queue/cursor left to rewind into) — so offering
          // an Undo action there would be a button that visibly does nothing. Omit the
          // action in that case; the confirmation toast itself still shows.
          toast({
            title: t('undo.title'),
            tone: grade === Grade.GRADE_AGAIN ? 'danger' : 'success',
            action: wasLastCard
              ? undefined
              : {
                  label: t('undo.action'),
                  onClick: () => {
                    undoLastGrade()
                    // Deliberately NOT re-invalidating GetCounts here: the backend's
                    // counts already reflect the original (un-undone) grade, so a
                    // re-fetch would return the same numbers — invalidating would just
                    // be a wasted network round-trip with no visible effect on screen.
                  },
                },
          })
        },
        onError: () => {
          setIsSubmitting(false)
          setGradeFlash(null)
          setAnswerError(true)
        },
      },
    )
  }

  // Clear the post-grade flash shortly after it's set — purely a CSS-transition-driven
  // visual cue, not something the advance()/mutation flow waits on.
  useEffect(() => {
    if (gradeFlash === null) {
      return
    }
    const timer = window.setTimeout(() => setGradeFlash(null), 200)
    return () => window.clearTimeout(timer)
  }, [gradeFlash])

  useEffect(() => {
    function handleKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') {
        event.preventDefault()
        navigate('/')
        return
      }
      if (isSubmitting) {
        return
      }
      if (isShowingFront && event.code === 'Space') {
        event.preventDefault()
        reveal()
        return
      }
      if (isShowingBack) {
        if (advancedGrading) {
          const button = GRADE_BUTTONS.find((b) => b.key === event.key)
          if (button) {
            event.preventDefault()
            submitGrade(button.grade)
          }
          return
        }
        if (event.key === '1') {
          event.preventDefault()
          submitGrade(Grade.GRADE_AGAIN)
        } else if (event.key === '2') {
          event.preventDefault()
          submitGrade(Grade.GRADE_GOOD)
        }
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isShowingFront, isShowingBack, isSubmitting, currentCard, advancedGrading, navigate])

  // Swipe gestures (pointer events, no gesture library): right = Know, left = Don't
  // Know. Only active in default 2-button mode while showingBack — advanced 4-grade
  // mode has no natural 2-direction mapping (brief item 5), and swiping the front card
  // has no defined meaning here (tap-to-reveal already covers that via CardFrame's
  // onReveal). Buttons remain visible/functional regardless (Constraint 5).
  const pointerStartX = useRef<number | null>(null)
  const swipeEnabled = isShowingBack && !advancedGrading

  function handlePointerDown(event: ReactPointerEvent<HTMLDivElement>) {
    if (!swipeEnabled) {
      return
    }
    pointerStartX.current = event.clientX
  }

  function handlePointerUp(event: ReactPointerEvent<HTMLDivElement>) {
    if (!swipeEnabled || pointerStartX.current === null) {
      return
    }
    const deltaX = event.clientX - pointerStartX.current
    pointerStartX.current = null
    if (Math.abs(deltaX) < SWIPE_THRESHOLD_PX) {
      return
    }
    if (deltaX > 0) {
      submitGrade(Grade.GRADE_GOOD)
    } else {
      submitGrade(Grade.GRADE_AGAIN)
    }
  }

  function handlePointerCancel() {
    pointerStartX.current = null
  }

  const flashClass = useMemo(() => {
    if (gradeFlash === 'know') {
      return 'ring-4 ring-success'
    }
    if (gradeFlash === 'dontKnow') {
      return 'ring-4 ring-danger'
    }
    return 'ring-0 ring-transparent'
  }, [gradeFlash])

  const header = (
    <header className="flex items-center justify-between gap-3 p-4">
      <div className="flex items-center gap-3">
        <IconButton label={t('exit')} icon={<ArrowLeftIcon />} onClick={() => navigate('/')} />
        <span className="text-sm font-medium text-fg">{deckData?.deck?.name ?? ''}</span>
      </div>
      {queueLength > 0 ? (
        <span className="text-sm text-muted">
          {t('progress', { current: cursorPosition, total: queueLength })}
        </span>
      ) : null}
      <div aria-hidden className="w-9" />
    </header>
  )

  if (state.status === 'loading') {
    return (
      <main className="flex min-h-screen items-center justify-center bg-bg">
        <p className="text-muted">{t('loading')}</p>
      </main>
    )
  }

  if (state.status === 'empty') {
    return (
      <main className="flex min-h-screen flex-col items-center justify-center gap-4 bg-bg">
        <p className="text-muted">{t('empty')}</p>
        <Link to="/" className="text-accent underline">
          {t('backToDecks')}
        </Link>
      </main>
    )
  }

  if (state.status === 'done') {
    return (
      <main className="flex min-h-screen flex-col items-center justify-center gap-4 bg-bg">
        <p className="text-muted">{t('deckComplete')}</p>
        <Link to="/" className="text-accent underline">
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
    <main className="flex min-h-screen flex-col bg-bg">
      {header}

      <div className="flex flex-1 items-center justify-center p-4">
        <div
          className={`w-full max-w-2xl rounded-[var(--radius-md)] transition-shadow duration-[var(--duration-base)] ease-default ${flashClass}`}
          onPointerDown={handlePointerDown}
          onPointerUp={handlePointerUp}
          onPointerCancel={handlePointerCancel}
        >
          {isShowingFront ? (
            <CardFrame html={currentCard.frontHtml} onReveal={reveal} />
          ) : (
            <CardFrame html={currentCard.frontHtml + (cardData?.backHtml ?? '')} />
          )}
        </div>
      </div>

      <div className="flex flex-col items-center gap-3 p-6">
        {answerError ? <p className="text-sm text-danger">{t('answerError')}</p> : null}

        {isShowingFront ? (
          <Button size="md" onClick={() => reveal()}>
            {t('showAnswer')}
          </Button>
        ) : advancedGrading ? (
          <div className="flex gap-2">
            {GRADE_BUTTONS.map(({ grade, labelKey }) => {
              const previewLabel = previewLabelFor(previewData, grade)
              const variant = grade === Grade.GRADE_AGAIN ? 'danger' : 'secondary'
              return (
                <Button
                  key={grade}
                  variant={variant}
                  disabled={isSubmitting}
                  onClick={() => submitGrade(grade)}
                  className="flex-col"
                >
                  <span>{t(labelKey)}</span>
                  {previewLabel ? <span className="text-xs opacity-80">{previewLabel}</span> : null}
                </Button>
              )
            })}
          </div>
        ) : (
          // 2-button default (brief item 7 / Constraint 7). Left/right assignment:
          // neither outcome is more "primary" than the other (both are equally valid
          // binary results of evaluating the card), so the placement instead follows
          // Constraint 11's consistency rule plus the existing GRADE_BUTTONS ordering
          // convention (worst-to-best, left-to-right: Again < Hard < Good < Easy) —
          // Don't Know (~Again) stays left, Know (~Good) stays right, matching where
          // each would sit in the 4-button advanced layout. This also matches swipe
          // direction (left = Don't Know, right = Know), keeping the two interaction
          // modes spatially consistent with each other.
          <div className="flex gap-3">
            <Button
              variant="danger"
              disabled={isSubmitting}
              onClick={() => submitGrade(Grade.GRADE_AGAIN)}
            >
              {t('grade.dontKnow')}
            </Button>
            <Button
              variant="success"
              disabled={isSubmitting}
              onClick={() => submitGrade(Grade.GRADE_GOOD)}
            >
              {t('grade.know')}
            </Button>
          </div>
        )}
      </div>
    </main>
  )
}
