import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  useAnalyticsServiceGetBreakdown,
  useAnalyticsServiceGetHeatmap,
  useAnalyticsServiceGetOverview,
  useAnalyticsServiceGetRetention,
} from '../../lib/api/analytics/analytics-service/analytics-service'
import type {
  V1BreakdownBucket,
  V1GetBreakdownResponse,
  V1GetHeatmapResponse,
  V1GetOverviewResponse,
  V1GetRetentionResponse,
  V1HeatmapDay,
} from '../../lib/api/analytics/analyticsV1AnalyticsProto.schemas'
import { Card, cn, EmptyState, Field, Select, Skeleton, Spinner } from '../../ui'

// Analytics dashboard (Task 8, see task-8-brief.md). Wires the Task 7 generated analytics
// client (overview/heatmap/retention/breakdown) into a single-screen stats view. Range/year/
// breakdown-by selection is local component state (not URL params) — this is read-only,
// single-screen MVP scope; URL persistence would only matter for deep-linking/sharing a
// specific view, which isn't a requirement here, and local state keeps the implementation
// simple per Constraint 2 (minimal cognitive load / no unnecessary complexity).
const RANGE_VALUES = ['7', '30', '90', '365', 'all'] as const
type RangeValue = (typeof RANGE_VALUES)[number]

const BREAKDOWN_BY_VALUES = ['deck', 'state', 'hour'] as const
type BreakdownBy = (typeof BREAKDOWN_BY_VALUES)[number]

// Year selector offers the current year plus two previous years. Three years is enough
// history for a spaced-repetition habit-tracking heatmap without cluttering the selector —
// most users won't have meaningful activity further back than that, and the backend has no
// "list available years" endpoint to size this dynamically.
const YEARS_BACK = 2

/**
 * Formats a milliseconds-as-string duration (GetOverview's wire format) into a simple
 * human-readable "Xh Ym" / "Ym" / "<1m" string. Intentionally simple for MVP — no full i18n
 * duration library, mirroring StudyScreen's formatIntervalSeconds judgment call.
 */
function formatDuration(timeMs: string | undefined): string {
  const n = Number(timeMs ?? '0')
  if (!Number.isFinite(n) || n <= 0) {
    return '0m'
  }
  const totalMinutes = Math.round(n / 60_000)
  if (totalMinutes < 1) {
    return '<1m'
  }
  const hours = Math.floor(totalMinutes / 60)
  const minutes = totalMinutes % 60
  if (hours === 0) {
    return `${minutes}m`
  }
  if (minutes === 0) {
    return `${hours}h`
  }
  return `${hours}h ${minutes}m`
}

/** Formats a 0..1 float as a whole-number percent string, e.g. 0.873 -> "87". */
function toPercent(value: number | undefined): number {
  if (value === undefined || !Number.isFinite(value)) {
    return 0
  }
  return Math.round(value * 100)
}

/**
 * Buckets a day's review count into one of 5 heatmap intensity levels (0 = no activity, 1-4
 * = increasing activity), GitHub-contribution-graph style. Thresholds are arbitrary but
 * reasonable defaults for an MVP without real usage data to calibrate against: 0, 1-2, 3-5,
 * 6-10, 11+.
 */
function heatmapLevel(reviews: number): 0 | 1 | 2 | 3 | 4 {
  if (reviews <= 0) return 0
  if (reviews <= 2) return 1
  if (reviews <= 5) return 2
  if (reviews <= 10) return 3
  return 4
}

// Level 0 (no activity) renders as a neutral gray cell (bg-border), matching GitHub/LeetCode
// where empty days are gray and only active days carry color; levels 1-4 ramp the semantic
// "success" green (studying is a positive state, per the design law in index.css) via opacity.
const HEATMAP_LEVEL_OPACITY: Record<0 | 1 | 2 | 3 | 4, number> = {
  0: 0,
  1: 0.4,
  2: 0.6,
  3: 0.8,
  4: 1,
}

interface HeatmapCell {
  /** ISO YYYY-MM-DD, or '' for an out-of-range padding cell (rendered as an empty spacer). */
  date: string
  reviews: number
}

interface HeatmapModel {
  weeks: HeatmapCell[][]
  /** The week-column index at which each new month first appears, for the month-label row. */
  months: { weekIndex: number; monthIso: string }[]
  /** Total reviews across the visible window (for the "N reviews …" subtitle). */
  totalReviews: number
}

/** Midnight-UTC start of the given date's calendar day. */
function startOfUtcDay(d: Date): Date {
  return new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate()))
}

/**
 * Builds a Sunday-start weekday × week grid (GitHub/LeetCode contribution-graph layout).
 *
 *   - mode 'current' → a rolling trailing 12-month window ending *today*: the last cell is
 *     today and the first is 364 days earlier, so the newest square is always the current day.
 *   - mode <year>    → that full calendar year (Jan 1 – Dec 31), for browsing history.
 *
 * Leading days are padded so column 0 starts on a Sunday; trailing days (including any days
 * after today in the final week) are padded so the last column is a full 7 rows. All dates
 * are UTC, matching the backend's UTC day bucketing.
 */
function buildHeatmapModel(
  mode: 'current' | number,
  today: Date,
  daysByDate: Map<string, number>,
): HeatmapModel {
  let start: Date
  let end: Date
  if (mode === 'current') {
    end = startOfUtcDay(today)
    start = startOfUtcDay(today)
    start.setUTCDate(start.getUTCDate() - 364)
  } else {
    start = new Date(Date.UTC(mode, 0, 1))
    end = new Date(Date.UTC(mode, 11, 31))
  }

  const cells: HeatmapCell[] = []
  for (let i = 0; i < start.getUTCDay(); i++) {
    cells.push({ date: '', reviews: -1 }) // leading padding so column 0 starts on Sunday
  }
  let totalReviews = 0
  for (let d = new Date(start); d <= end; d.setUTCDate(d.getUTCDate() + 1)) {
    const iso = d.toISOString().slice(0, 10)
    const reviews = daysByDate.get(iso) ?? 0
    cells.push({ date: iso, reviews })
    totalReviews += reviews
  }
  while (cells.length % 7 !== 0) {
    cells.push({ date: '', reviews: -1 }) // trailing padding to complete the final week column
  }

  const weeks: HeatmapCell[][] = []
  for (let i = 0; i < cells.length; i += 7) {
    weeks.push(cells.slice(i, i + 7))
  }

  const months: { weekIndex: number; monthIso: string }[] = []
  let lastMonth = -1
  weeks.forEach((week, weekIndex) => {
    const firstReal = week.find((c) => c.date !== '')
    if (!firstReal) return
    const month = Number(firstReal.date.slice(5, 7))
    if (month !== lastMonth) {
      months.push({ weekIndex, monthIso: firstReal.date })
      lastMonth = month
    }
  })

  return { weeks, months, totalReviews }
}

export function StatsScreen() {
  const { t, i18n } = useTranslation('stats')
  // Computed once per mount: `today` anchors the rolling heatmap window and `currentYear`
  // (its UTC year) is what the "Current" selection resolves to.
  const today = useMemo(() => new Date(), [])
  const currentYear = today.getUTCFullYear()
  const [range, setRange] = useState<RangeValue>('30')
  // Heatmap selection: 'current' = rolling trailing 12 months ending today; a number = that
  // full calendar year.
  const [yearSel, setYearSel] = useState<'current' | number>('current')
  const [breakdownBy, setBreakdownBy] = useState<BreakdownBy>('deck')
  const primaryYear = yearSel === 'current' ? currentYear : yearSel

  // The analytics API expects the range as "<N>d" (e.g. "30d") or "all"; the selector keeps
  // the bare keys ('7', '30', …) for its labels, so convert to the wire format at the boundary.
  const apiRange = range === 'all' ? 'all' : `${range}d`

  const overviewQuery = useAnalyticsServiceGetOverview({ range: apiRange })
  // The rolling trailing-year window spans two calendar years, and the backend serves one
  // calendar year per call, so fetch the primary year and the one before it and merge.
  const heatmapQuery = useAnalyticsServiceGetHeatmap({ year: primaryYear })
  const heatmapPrevQuery = useAnalyticsServiceGetHeatmap({ year: primaryYear - 1 })
  const retentionQuery = useAnalyticsServiceGetRetention({ range: apiRange })
  const breakdownQuery = useAnalyticsServiceGetBreakdown({ by: breakdownBy, range: apiRange })

  // See DecksScreen.tsx / StudyScreen.tsx for the established pattern: customFetch already
  // throws on non-2xx, so by the time `data` is defined here it's always the success shape.
  const overview = overviewQuery.data?.data as V1GetOverviewResponse | undefined
  const heatmap = heatmapQuery.data?.data as V1GetHeatmapResponse | undefined
  const heatmapPrev = heatmapPrevQuery.data?.data as V1GetHeatmapResponse | undefined
  const retention = retentionQuery.data?.data as V1GetRetentionResponse | undefined
  const breakdown = breakdownQuery.data?.data as V1GetBreakdownResponse | undefined

  const isOverviewLoading = overviewQuery.isPending
  // Empty-state condition: the user has no review history at all yet (reviews is "0" or
  // absent on the overview for the selected range). Once overview has loaded and confirms
  // zero reviews, show a single invitational empty state instead of every section's own
  // (separately loading/empty) UI — there's nothing meaningful to show in any of them yet.
  const hasNoHistory =
    !isOverviewLoading && (overview?.reviews === undefined || overview.reviews === '0')

  const daysByDate = useMemo(() => {
    const map = new Map<string, number>()
    for (const response of [heatmapPrev, heatmap]) {
      for (const day of (response?.days ?? []) as V1HeatmapDay[]) {
        if (day.date) {
          map.set(day.date, Number(day.reviews ?? '0'))
        }
      }
    }
    return map
  }, [heatmap, heatmapPrev])
  const heatmapModel = useMemo(
    () => buildHeatmapModel(yearSel, today, daysByDate),
    [yearSel, today, daysByDate],
  )
  // Localized short month names (UTC, to match the day buckets) for the month-label row.
  const monthFmt = useMemo(
    () => new Intl.DateTimeFormat(i18n.language, { month: 'short', timeZone: 'UTC' }),
    [i18n.language],
  )
  const monthLabelByWeek = useMemo(() => {
    const map = new Map<number, string>()
    for (const m of heatmapModel.months) {
      map.set(m.weekIndex, monthFmt.format(new Date(`${m.monthIso}T00:00:00Z`)))
    }
    return map
  }, [heatmapModel.months, monthFmt])

  const buckets = (breakdown?.buckets ?? []) as V1BreakdownBucket[]
  const maxBucketReviews = Math.max(1, ...buckets.map((b) => Number(b.reviews ?? '0')))

  // Past calendar years available in the heatmap selector; the current period is the
  // separate "Current" rolling-window option.
  const pastYears = useMemo(
    () => Array.from({ length: YEARS_BACK }, (_, i) => currentYear - 1 - i),
    [currentYear],
  )

  if (isOverviewLoading) {
    return (
      <div className="flex flex-col gap-6 p-6" aria-busy="true">
        <h1 className="text-xl font-semibold text-fg">{t('title')}</h1>
        <Spinner label={t('loading')} />
        <Skeleton className="h-10 w-48" />
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <Skeleton className="h-24 w-full" />
          <Skeleton className="h-24 w-full" />
          <Skeleton className="h-24 w-full" />
          <Skeleton className="h-24 w-full" />
        </div>
        <Skeleton className="h-40 w-full" />
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-40 w-full" />
      </div>
    )
  }

  if (hasNoHistory) {
    return (
      <div className="flex flex-col gap-6 p-6">
        <h1 className="text-xl font-semibold text-fg">{t('title')}</h1>
        <EmptyState title={t('empty')} description={t('emptyDescription')} />
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between gap-3">
        <h1 className="text-xl font-semibold text-fg">{t('title')}</h1>
        <div className="w-44">
          <Field label={t('range.label')}>
            {(controlProps) => (
              <Select
                {...controlProps}
                value={range}
                onChange={(e) => setRange(e.target.value as RangeValue)}
              >
                {RANGE_VALUES.map((value) => (
                  <option key={value} value={value}>
                    {t(`range.${value}`)}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        </div>
      </div>

      {/* Overview KPI cards */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Card>
          <p className="text-sm text-muted">{t('overview.reviews')}</p>
          <p className="text-2xl font-semibold text-fg">{overview?.reviews ?? '0'}</p>
        </Card>
        <Card>
          <p className="text-sm text-muted">{t('overview.accuracy')}</p>
          <p className="text-2xl font-semibold text-fg">{toPercent(overview?.accuracy)}%</p>
        </Card>
        <Card>
          <p className="text-sm text-muted">{t('overview.streak')}</p>
          <p className="text-2xl font-semibold text-fg">
            {t('overview.streakValue', { count: overview?.streakDays ?? 0 })}
          </p>
        </Card>
        <Card>
          <p className="text-sm text-muted">{t('overview.timeStudied')}</p>
          <p className="text-2xl font-semibold text-fg">{formatDuration(overview?.timeMs)}</p>
        </Card>
      </div>

      {/* Contribution heatmap */}
      <Card className="flex flex-col gap-3">
        <div className="flex items-start justify-between gap-3">
          <div className="flex flex-col gap-0.5">
            <h2 className="text-base font-semibold text-fg">{t('heatmap.title')}</h2>
            <p className="text-sm text-muted">
              {yearSel === 'current'
                ? t('heatmap.summary', { count: heatmapModel.totalReviews })
                : t('heatmap.summaryYear', { count: heatmapModel.totalReviews, year: yearSel })}
            </p>
          </div>
          <div className="w-32">
            <Field label={t('heatmap.year')}>
              {(controlProps) => (
                <Select
                  {...controlProps}
                  value={String(yearSel)}
                  onChange={(e) =>
                    setYearSel(e.target.value === 'current' ? 'current' : Number(e.target.value))
                  }
                >
                  <option value="current">{t('heatmap.current')}</option>
                  {pastYears.map((y) => (
                    <option key={y} value={y}>
                      {y}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
          </div>
        </div>
        {heatmapQuery.isPending || heatmapPrevQuery.isPending ? (
          <Skeleton className="h-32 w-full" />
        ) : (
          <div className="flex flex-col gap-2">
            {/* No overriding role on the container or the day cells: each day cell's
                aria-label must remain individually reachable by assistive tech. A
                role="img" on the container would flatten the accessibility tree and
                make per-day labels unreachable (see task-8 review finding). */}
            <div className="w-full overflow-x-auto">
              <div className="inline-flex flex-col gap-1">
                {/* Month labels, aligned to the week columns they begin on. */}
                <div className="flex gap-[3px]">
                  {heatmapModel.weeks.map((_, weekIndex) => (
                    <div key={weekIndex} className="relative h-4 w-3">
                      {monthLabelByWeek.has(weekIndex) && (
                        <span className="absolute left-0 top-0 whitespace-nowrap text-xs text-muted">
                          {monthLabelByWeek.get(weekIndex)}
                        </span>
                      )}
                    </div>
                  ))}
                </div>
                {/* Week columns (Sunday at top), newest week on the right, today last. */}
                <div className="flex gap-[3px]">
                  {heatmapModel.weeks.map((week, weekIndex) => (
                    <div key={weekIndex} className="flex flex-col gap-[3px]">
                      {week.map((cell, dayIndex) =>
                        cell.date === '' ? (
                          <div key={dayIndex} className="h-3 w-3" aria-hidden="true" />
                        ) : (
                          <div
                            key={cell.date}
                            aria-label={t('heatmap.dayLabel', {
                              date: cell.date,
                              count: cell.reviews,
                            })}
                            title={t('heatmap.dayLabel', { date: cell.date, count: cell.reviews })}
                            className={cn(
                              'h-3 w-3 rounded-[2px]',
                              cell.reviews > 0 ? 'bg-success' : 'bg-border',
                            )}
                            style={
                              cell.reviews > 0
                                ? { opacity: HEATMAP_LEVEL_OPACITY[heatmapLevel(cell.reviews)] }
                                : undefined
                            }
                          />
                        ),
                      )}
                    </div>
                  ))}
                </div>
              </div>
            </div>
            <div className="flex items-center gap-2 text-xs text-muted">
              <span>{t('heatmap.legendLess')}</span>
              <div className="flex gap-[3px]">
                <div className="h-3 w-3 rounded-[2px] bg-border" aria-hidden="true" />
                {([1, 2, 3, 4] as const).map((level) => (
                  <div
                    key={level}
                    aria-hidden="true"
                    className="h-3 w-3 rounded-[2px] bg-success"
                    style={{ opacity: HEATMAP_LEVEL_OPACITY[level] }}
                  />
                ))}
              </div>
              <span>{t('heatmap.legendMore')}</span>
            </div>
          </div>
        )}
      </Card>

      {/* Retention vs target */}
      <Card className="flex flex-col gap-3">
        <h2 className="text-base font-semibold text-fg">{t('retention.title')}</h2>
        {retentionQuery.isPending ? (
          <Skeleton className="h-16 w-full" />
        ) : (
          <div className="flex flex-col gap-2">
            <div className="flex items-center justify-between text-sm text-muted">
              <span>
                {t('retention.measured')}: {toPercent(retention?.measured)}%
              </span>
              <span>
                {t('retention.target')}: {toPercent(retention?.target)}%
              </span>
            </div>
            <div className="relative h-3 w-full overflow-hidden rounded-full bg-border">
              <div
                className="h-full rounded-full bg-accent"
                style={{ width: `${Math.min(100, toPercent(retention?.measured))}%` }}
              />
              <div
                className="absolute top-0 h-full w-0.5 bg-fg"
                style={{ left: `${Math.min(100, toPercent(retention?.target))}%` }}
                aria-hidden="true"
              />
            </div>
            <p className="text-sm text-muted">
              {t('retention.dueContext', {
                correct: retention?.dueCorrect ?? '0',
                reviews: retention?.dueReviews ?? '0',
              })}
            </p>
          </div>
        )}
      </Card>

      {/* Breakdown */}
      <Card className="flex flex-col gap-3">
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-base font-semibold text-fg">{t('breakdown.title')}</h2>
          <div className="w-36">
            <Field label={t('breakdown.by')}>
              {(controlProps) => (
                <Select
                  {...controlProps}
                  value={breakdownBy}
                  onChange={(e) => setBreakdownBy(e.target.value as BreakdownBy)}
                >
                  {BREAKDOWN_BY_VALUES.map((value) => (
                    <option key={value} value={value}>
                      {t(`breakdown.${value}`)}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
          </div>
        </div>
        {breakdownQuery.isPending ? (
          <Skeleton className="h-32 w-full" />
        ) : buckets.length === 0 ? (
          <p className="text-sm text-muted">{t('breakdown.empty')}</p>
        ) : (
          <ul className="flex flex-col gap-2">
            {buckets.map((bucket) => {
              const reviews = Number(bucket.reviews ?? '0')
              const correct = Number(bucket.correct ?? '0')
              const accuracyPercent = reviews > 0 ? Math.round((correct / reviews) * 100) : 0
              const widthPercent = (reviews / maxBucketReviews) * 100
              return (
                <li key={bucket.key} className="flex flex-col gap-1">
                  <div className="flex items-center justify-between text-sm text-fg">
                    <span className="font-medium">{bucket.key}</span>
                    <span className="text-muted">
                      {t('breakdown.reviewsCount', { count: reviews })} ·{' '}
                      {t('breakdown.accuracy', { percent: accuracyPercent })}
                    </span>
                  </div>
                  <div className="h-2 w-full overflow-hidden rounded-full bg-border">
                    <div
                      className="h-full rounded-full bg-accent"
                      style={{ width: `${widthPercent}%` }}
                    />
                  </div>
                </li>
              )
            })}
          </ul>
        )}
      </Card>
    </div>
  )
}
