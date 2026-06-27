import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { useSearchServiceSearch } from '../../lib/api/search/search-service/search-service'
import type {
  V1CardSummary,
  V1SearchResponse,
} from '../../lib/api/search/searchV1SearchProto.schemas'
import { Badge, Button, EmptyState, Field, Skeleton, Spinner, TextInput } from '../../ui'
import type { BadgeTone } from '../../ui'
import { CardDetail } from './CardDetail.tsx'

// Search / card browser (Task 9, see task-9-brief.md). Wires the Task 7 generated search
// client (DSL query over the caller's cards, keyset-paginated) into a single-screen browse
// view. Row click hands off to Task 10's (not yet built) card detail dialog via an optional
// `onSelectCard` prop — see the judgment call below.

// Debounce delay for the search input. No existing debounce utility/hook in this codebase
// (checked), so this is a small local debounce rather than a new dependency. 300ms is a
// reasonable default that keeps typing responsive while avoiding a request per keystroke —
// not tied to any measured backend latency, just a common, unobtrusive value.
const DEBOUNCE_MS = 300

// Page size for each search request / "Load more" page. Arbitrary but reasonable: large
// enough to feel substantial, small enough that "Load more" stays meaningful rather than a
// single giant page.
const PAGE_SIZE = 20

/**
 * Maps a card's `state` string (e.g. "new"/"learning"/"review"/"suspended", or `suspended:
 * true` overriding state display) onto a Badge tone. `suspended` takes priority over state
 * since a suspended card isn't actively being scheduled regardless of its underlying FSRS
 * state — showing it as e.g. "Review" would be misleading about why it never comes up.
 * Tone mapping: new -> neutral (not yet started, informational), learning -> accent (the
 * single brand accent, "in progress"), review -> success (graduated, the desired end state),
 * suspended -> danger (explicitly paused, draws attention). Unknown/missing state falls back
 * to neutral.
 */
function stateTone(state: string | undefined, suspended: boolean | undefined): BadgeTone {
  if (suspended) return 'danger'
  switch (state) {
    case 'learning':
      return 'accent'
    case 'review':
      return 'success'
    case 'new':
    default:
      return 'neutral'
  }
}

// Hard character-length cut for the fields-text preview rather than CSS line-clamp: fieldsText
// can contain raw HTML from note templates, and clamping with CSS while rendering as plain
// text (see below — we intentionally don't render it as HTML, to avoid injecting untrusted
// markup) can still leave a very long single line measured oddly across browsers. A simple
// character cut is predictable and trivial to test.
const PREVIEW_MAX_CHARS = 140

function truncatePreview(text: string): string {
  if (text.length <= PREVIEW_MAX_CHARS) return text
  return `${text.slice(0, PREVIEW_MAX_CHARS).trimEnd()}…`
}

export interface BrowseScreenProps {
  /**
   * Called when a result row is activated. Defaults (when not passed in, e.g. from the
   * router) to opening this screen's own internal CardDetail dialog — see the Task 10
   * judgment call below. An explicit prop is still accepted so a future different call
   * site can override the behavior, but the router wiring no longer needs to do anything
   * special: BrowseScreen is self-contained.
   */
  onSelectCard?: (cardId: string) => void
}

export function BrowseScreen({ onSelectCard }: BrowseScreenProps = {}) {
  const { t } = useTranslation('browse')
  // Task 10 judgment call (wiring approach (a) from the brief): rather than having the
  // router wrap BrowseScreen in a stateful parent just to hold `selectedCardId`, the
  // screen owns that state itself and renders CardDetail inline. This keeps the change
  // localized to this file and the router untouched, and CardDetail remains independently
  // reusable from any other future call site since it only needs a `cardId` + `onClose`.
  const [selectedCardId, setSelectedCardId] = useState<string | null>(null)
  const handleSelectCard = onSelectCard ?? setSelectedCardId
  const [query, setQuery] = useState('')
  const [debouncedQuery, setDebouncedQuery] = useState('')
  // Stack of cursors consumed so far; `undefined` means "first page". Accumulated results are
  // kept separately from the latest query response so "Load more" appends rather than
  // replacing already-rendered rows.
  const [cursor, setCursor] = useState<string | undefined>(undefined)
  const [accumulated, setAccumulated] = useState<V1CardSummary[]>([])
  // Tracks the debounced query a page reset has already been applied for, so resetting
  // pagination on a query change can happen during render (React's "adjusting state during
  // render" pattern, see react.dev) rather than in a useEffect — avoiding the
  // setState-in-effect cascading-render lint rule for a synchronous, render-time derivation.
  const [resetForQuery, setResetForQuery] = useState('')

  // Debounce: typing updates `query` immediately (so the input feels responsive) but the
  // value that actually feeds the search param only updates after the user pauses typing.
  useEffect(() => {
    const timeout = setTimeout(() => {
      setDebouncedQuery(query)
    }, DEBOUNCE_MS)
    return () => clearTimeout(timeout)
  }, [query])

  // A new debounced query always restarts pagination from the first page and clears
  // previously accumulated results — they belong to the old query. Applied during render
  // (not an effect) since it's a pure derivation from props/state, not a synchronization with
  // an external system.
  let effectiveCursor = cursor
  let effectiveAccumulated = accumulated
  if (resetForQuery !== debouncedQuery) {
    effectiveCursor = undefined
    effectiveAccumulated = []
    setResetForQuery(debouncedQuery)
    setCursor(undefined)
    setAccumulated([])
  }

  // Empty query still calls the search endpoint (an empty `q` is a valid, reasonable request
  // per the brief — the backend returns the most-recent cards, which is a sensible default
  // "browse" view rather than an artificially blank screen before the user types anything).
  const searchQuery = useSearchServiceSearch({
    q: debouncedQuery || undefined,
    cursor: effectiveCursor,
    limit: PAGE_SIZE,
  })

  const response = searchQuery.data?.data as V1SearchResponse | undefined
  // Tracks the response object already merged into `accumulated`, so the merge below only
  // runs once per new response (during render, not an effect) instead of on every render.
  const [mergedResponse, setMergedResponse] = useState<V1SearchResponse | undefined>(undefined)

  let results = effectiveAccumulated
  if (response && response !== mergedResponse) {
    results =
      effectiveCursor === undefined
        ? (response.results ?? [])
        : [...effectiveAccumulated, ...(response.results ?? [])]
    setMergedResponse(response)
    setAccumulated(results)
  }

  const isInitialLoading = searchQuery.isPending
  const isLoadingMore = searchQuery.isFetching && !isInitialLoading && effectiveCursor !== undefined
  const nextCursor = response?.nextCursor

  function handleLoadMore() {
    if (nextCursor) {
      setCursor(nextCursor)
    }
  }

  return (
    <div className="flex flex-col gap-6 p-6">
      <h1 className="text-xl font-semibold text-fg">{t('title')}</h1>

      <Field label={t('search.label')} help={t('search.help')}>
        {(controlProps) => (
          <TextInput
            {...controlProps}
            type="search"
            placeholder={t('search.placeholder')}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        )}
      </Field>

      {isInitialLoading ? (
        <div className="flex flex-col gap-2" aria-busy="true">
          <Spinner label={t('loading')} />
          <Skeleton className="h-20 w-full" />
          <Skeleton className="h-20 w-full" />
          <Skeleton className="h-20 w-full" />
        </div>
      ) : searchQuery.isError ? (
        <EmptyState
          title={t('error')}
          description={t('errorDescription')}
          action={<Button onClick={() => void searchQuery.refetch()}>{t('retry')}</Button>}
        />
      ) : results.length === 0 ? (
        // Reached for any completed, non-error query with zero results — including an empty
        // `q` that legitimately returns nothing (e.g. a brand-new account). See the
        // empty-query judgment call above: the endpoint is always called, so this branch
        // always means "a real response came back empty," not "no query was made yet."
        <EmptyState title={t('empty')} description={t('emptyDescription')} />
      ) : (
        <div className="flex flex-col gap-3">
          <ul className="flex flex-col gap-2">
            {results.map((card) => (
              <li key={card.cardId}>
                <button
                  type="button"
                  onClick={() => card.cardId && handleSelectCard(card.cardId)}
                  aria-label={t('resultLabel')}
                  className="flex w-full flex-col gap-1.5 rounded-[var(--radius-md)] border border-border bg-surface p-3 text-left transition-colors duration-[var(--duration-fast)] ease-default hover:bg-accent-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-bg"
                >
                  <div className="flex items-center justify-between gap-3">
                    <span className="truncate text-sm font-medium text-fg">{card.deckPath}</span>
                    <Badge tone={stateTone(card.state, card.suspended)}>
                      {card.suspended
                        ? t('state.suspended')
                        : card.state
                          ? t(`state.${card.state}`)
                          : t('state.new')}
                    </Badge>
                  </div>
                  {card.tags && card.tags.length > 0 ? (
                    <div className="flex flex-wrap gap-1">
                      {card.tags.map((tag) => (
                        <Badge key={tag} tone="neutral">
                          {tag}
                        </Badge>
                      ))}
                    </div>
                  ) : null}
                  <p className="text-sm text-muted">
                    {card.fieldsText ? truncatePreview(card.fieldsText) : t('preview.noText')}
                  </p>
                  <div className="flex gap-3 text-xs text-muted">
                    <span>{t('stats.interval', { count: card.ivl ?? 0 })}</span>
                    <span>{t('stats.reps', { count: card.reps ?? 0 })}</span>
                  </div>
                </button>
              </li>
            ))}
          </ul>

          {nextCursor ? (
            <div className="flex items-center gap-3">
              <Button variant="secondary" onClick={handleLoadMore} disabled={isLoadingMore}>
                {t('loadMore')}
              </Button>
              {isLoadingMore ? <Spinner size={16} label={t('loadingMore')} /> : null}
            </div>
          ) : null}
        </div>
      )}

      {/* Only this screen's own internal selection state drives its own dialog — when a
          caller passes `onSelectCard`, they've taken ownership of "what happens on
          select" and are presumably rendering their own CardDetail (or something else)
          elsewhere, so this screen shouldn't also pop one up itself. */}
      {onSelectCard ? null : (
        <CardDetail cardId={selectedCardId} onClose={() => setSelectedCardId(null)} />
      )}
    </div>
  )
}
