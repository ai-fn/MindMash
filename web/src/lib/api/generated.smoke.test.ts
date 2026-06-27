import { describe, expect, it } from 'vitest'

import { useAuthServiceLogin } from './identity/auth-service/auth-service'
import {
  getDeckServiceListDecksQueryKey,
  useDeckServiceListDecks,
} from './collection/deck-service/deck-service'
import { useCardServiceGetCard } from './collection/card-service/card-service'
import {
  useStudyServiceAnswer,
  useStudyServiceGetCounts,
  useStudyServiceGetQueue,
} from './scheduler/study-service/study-service'
import {
  useAnalyticsServiceGetBreakdown,
  useAnalyticsServiceGetHeatmap,
  useAnalyticsServiceGetOverview,
  useAnalyticsServiceGetRetention,
} from './analytics/analytics-service/analytics-service'
import { useSearchServiceSearch } from './search/search-service/search-service'

// Smoke test that the orval-generated client is wired correctly: every hook the
// app needs exists and is the expected shape, and a query-key factory returns the
// key the rest of the app relies on for cache invalidation (see task-2-brief.md).
describe('generated API client', () => {
  it('exports the hooks the study-loop MVP depends on', () => {
    expect(typeof useAuthServiceLogin).toBe('function')
    expect(typeof useDeckServiceListDecks).toBe('function')
    expect(typeof useCardServiceGetCard).toBe('function')
    expect(typeof useStudyServiceGetQueue).toBe('function')
    expect(typeof useStudyServiceGetCounts).toBe('function')
    expect(typeof useStudyServiceAnswer).toBe('function')
    expect(typeof useAnalyticsServiceGetOverview).toBe('function')
    expect(typeof useAnalyticsServiceGetHeatmap).toBe('function')
    expect(typeof useAnalyticsServiceGetRetention).toBe('function')
    expect(typeof useAnalyticsServiceGetBreakdown).toBe('function')
    expect(typeof useSearchServiceSearch).toBe('function')
  })

  it('generates a stable query key for listDecks', () => {
    expect(getDeckServiceListDecksQueryKey()).toEqual(['/v1/decks'])
  })
})
