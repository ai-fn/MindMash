import { useTranslation } from 'react-i18next'

import { useStudyServiceGetCounts } from '../../lib/api/scheduler/study-service/study-service'

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

  return (
    <div className="flex items-center justify-between rounded border border-slate-200 bg-white px-4 py-3">
      <span className="font-medium text-slate-800">{name}</span>
      {!isPending && (
        <span className="flex gap-3 text-sm text-slate-600">
          <span>{t('counts.new', { count: data?.data.newCount ?? 0 })}</span>
          <span>{t('counts.learning', { count: data?.data.learningCount ?? 0 })}</span>
          <span>{t('counts.review', { count: data?.data.reviewCount ?? 0 })}</span>
        </span>
      )}
    </div>
  )
}
