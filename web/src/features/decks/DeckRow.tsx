import { useTranslation } from 'react-i18next'

import { useStudyServiceGetCounts } from '../../lib/api/scheduler/study-service/study-service'
import type { V1GetCountsResponse } from '../../lib/api/scheduler/schedulerV1SchedulerProto.schemas'
import { Badge, Skeleton, Surface } from '../../ui'

interface DeckRowProps {
  deckId: string
  name: string
}

// Presentational row: deck name + its study counts as quiet badges (Constraint 3 —
// counts are informational, not decorative). Counts are fetched per-row (N+1 — there
// is no bulk-counts endpoint, see task-4-brief.md) so a slow/failed counts call for
// one deck never blocks the rest of the list from rendering (React Query isolates
// each hook instance's request/error state per component).
export function DeckRow({ deckId, name }: DeckRowProps) {
  const { t } = useTranslation('decks')
  const { data, isPending } = useStudyServiceGetCounts(deckId)

  // See DecksScreen.tsx's comment: `data.data` is always the success shape by the
  // time this resolves, since customFetch throws on any non-2xx response.
  const counts = data?.data as V1GetCountsResponse | undefined

  return (
    <Surface className="flex items-center justify-between gap-3 px-4 py-3">
      <span className="font-medium text-fg">{name}</span>
      {isPending ? (
        <Skeleton className="h-5 w-32" />
      ) : (
        <span className="flex items-center gap-1.5">
          <Badge tone="accent">{t('counts.new', { count: counts?.newCount ?? 0 })}</Badge>
          <Badge tone="neutral">
            {t('counts.learning', { count: counts?.learningCount ?? 0 })}
          </Badge>
          <Badge tone="neutral">{t('counts.review', { count: counts?.reviewCount ?? 0 })}</Badge>
        </span>
      )}
    </Surface>
  )
}
