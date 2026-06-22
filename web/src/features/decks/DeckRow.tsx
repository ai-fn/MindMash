import { useTranslation } from 'react-i18next'

import { useStudyServiceGetCounts } from '../../lib/api/scheduler/study-service/study-service'
import type { V1GetCountsResponse } from '../../lib/api/scheduler/schedulerV1SchedulerProto.schemas'

interface DeckRowProps {
  deckId: string
  name: string
}

// Presentational row: deck name + its study counts. Counts are fetched per-row
// (N+1 — there is no bulk-counts endpoint, see task-4-brief.md) so a slow/failed
// counts call for one deck never blocks the rest of the list from rendering.
export function DeckRow({ deckId, name }: DeckRowProps) {
  const { t } = useTranslation('decks')
  const { data, isPending } = useStudyServiceGetCounts(deckId)

  // See DecksScreen.tsx's comment: `data.data` is always the success shape by the
  // time this resolves, since customFetch throws on any non-2xx response.
  const counts = data?.data as V1GetCountsResponse | undefined

  return (
    <div className="flex items-center justify-between rounded border border-slate-200 bg-white px-4 py-3">
      <span className="font-medium text-slate-800">{name}</span>
      {!isPending && (
        <span className="flex gap-3 text-sm text-slate-600">
          <span>{t('counts.new', { count: counts?.newCount ?? 0 })}</span>
          <span>{t('counts.learning', { count: counts?.learningCount ?? 0 })}</span>
          <span>{t('counts.review', { count: counts?.reviewCount ?? 0 })}</span>
        </span>
      )}
    </div>
  )
}
