import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import '../../lib/i18n.ts'

// Mock the generated analytics hooks StatsScreen consumes, following the same
// per-module vi.mock pattern DecksScreen.test.tsx / StudyScreen.test.tsx use for
// generated hooks.
vi.mock('../../lib/api/analytics/analytics-service/analytics-service', () => ({
  useAnalyticsServiceGetOverview: vi.fn(),
  useAnalyticsServiceGetHeatmap: vi.fn(),
  useAnalyticsServiceGetRetention: vi.fn(),
  useAnalyticsServiceGetBreakdown: vi.fn(),
}))

async function renderScreen() {
  const { StatsScreen } = await import('./StatsScreen.tsx')
  // Seed a registration date so the heatmap's Year selector offers past-year options (it lists
  // every year from the earliest profile's createdAt up to last year). Set on the same fresh
  // authStore instance StatsScreen resolves after resetModules — see SettingsScreen.test.tsx.
  const { useAuthStore } = await import('../auth/authStore')
  useAuthStore.setState({ profiles: [{ id: 'p1', createdAt: '2020-01-01T00:00:00Z' }] })
  const queryClient = new QueryClient()
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <StatsScreen />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

const populatedOverview = {
  data: {
    data: { reviews: '120', correct: '100', accuracy: 0.833, timeMs: '8100000', streakDays: 5 },
  },
  isPending: false,
}

const populatedHeatmap = {
  data: {
    data: {
      days: [
        { date: '2026-01-01', reviews: '3' },
        { date: '2026-01-02', reviews: '0' },
      ],
    },
  },
  isPending: false,
}

const populatedRetention = {
  data: { data: { measured: 0.87, target: 0.9, dueReviews: '50', dueCorrect: '43' } },
  isPending: false,
}

const populatedBreakdown = {
  data: {
    data: {
      buckets: [
        { key: 'Spanish', reviews: '80', correct: '70', timeMs: '500000' },
        { key: 'Japanese', reviews: '40', correct: '30', timeMs: '300000' },
      ],
    },
  },
  isPending: false,
}

describe('StatsScreen', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    // Pin "today" so the rolling trailing-year heatmap window is deterministic (the
    // populated-heatmap fixture dates 2026-01-01/02 fall inside a window ending 2026-07-04).
    // shouldAdvanceTime keeps userEvent's internal timers working under fake timers. Midday UTC
    // is 2026-07-04 locally in every zone within ±11h, since the heatmap follows local days.
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.setSystemTime(new Date('2026-07-04T12:00:00Z'))
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.resetModules()
  })

  it('shows the empty state inviting study when overview.reviews is "0"', async () => {
    const {
      useAnalyticsServiceGetOverview,
      useAnalyticsServiceGetHeatmap,
      useAnalyticsServiceGetRetention,
      useAnalyticsServiceGetBreakdown,
    } = await import('../../lib/api/analytics/analytics-service/analytics-service')
    vi.mocked(useAnalyticsServiceGetOverview).mockReturnValue({
      data: { data: { reviews: '0' } },
      isPending: false,
    } as never)
    vi.mocked(useAnalyticsServiceGetHeatmap).mockReturnValue({
      data: undefined,
      isPending: false,
    } as never)
    vi.mocked(useAnalyticsServiceGetRetention).mockReturnValue({
      data: undefined,
      isPending: false,
    } as never)
    vi.mocked(useAnalyticsServiceGetBreakdown).mockReturnValue({
      data: undefined,
      isPending: false,
    } as never)

    await renderScreen()

    expect(screen.getByText('No stats yet')).toBeInTheDocument()
    expect(screen.getByText('Study a few cards to see your stats.')).toBeInTheDocument()
    expect(screen.queryByText('Reviews')).not.toBeInTheDocument()
  })

  it("asks for days and hours in the viewer's time zone", async () => {
    const {
      useAnalyticsServiceGetOverview,
      useAnalyticsServiceGetHeatmap,
      useAnalyticsServiceGetRetention,
      useAnalyticsServiceGetBreakdown,
    } = await import('../../lib/api/analytics/analytics-service/analytics-service')
    vi.mocked(useAnalyticsServiceGetOverview).mockReturnValue(populatedOverview as never)
    vi.mocked(useAnalyticsServiceGetHeatmap).mockReturnValue(populatedHeatmap as never)
    vi.mocked(useAnalyticsServiceGetRetention).mockReturnValue(populatedRetention as never)
    vi.mocked(useAnalyticsServiceGetBreakdown).mockReturnValue(populatedBreakdown as never)

    await renderScreen()

    const tz = Intl.DateTimeFormat().resolvedOptions().timeZone
    expect(useAnalyticsServiceGetOverview).toHaveBeenCalledWith(expect.objectContaining({ tz }))
    expect(useAnalyticsServiceGetHeatmap).toHaveBeenCalledWith(expect.objectContaining({ tz }))
    expect(useAnalyticsServiceGetBreakdown).toHaveBeenCalledWith(expect.objectContaining({ tz }))
  })

  it('shows the empty state when overview.reviews is absent', async () => {
    const {
      useAnalyticsServiceGetOverview,
      useAnalyticsServiceGetHeatmap,
      useAnalyticsServiceGetRetention,
      useAnalyticsServiceGetBreakdown,
    } = await import('../../lib/api/analytics/analytics-service/analytics-service')
    vi.mocked(useAnalyticsServiceGetOverview).mockReturnValue({
      data: { data: {} },
      isPending: false,
    } as never)
    vi.mocked(useAnalyticsServiceGetHeatmap).mockReturnValue({
      data: undefined,
      isPending: false,
    } as never)
    vi.mocked(useAnalyticsServiceGetRetention).mockReturnValue({
      data: undefined,
      isPending: false,
    } as never)
    vi.mocked(useAnalyticsServiceGetBreakdown).mockReturnValue({
      data: undefined,
      isPending: false,
    } as never)

    await renderScreen()

    expect(screen.getByText('No stats yet')).toBeInTheDocument()
  })

  it('shows loading skeletons while overview is pending', async () => {
    const {
      useAnalyticsServiceGetOverview,
      useAnalyticsServiceGetHeatmap,
      useAnalyticsServiceGetRetention,
      useAnalyticsServiceGetBreakdown,
    } = await import('../../lib/api/analytics/analytics-service/analytics-service')
    vi.mocked(useAnalyticsServiceGetOverview).mockReturnValue({
      data: undefined,
      isPending: true,
    } as never)
    vi.mocked(useAnalyticsServiceGetHeatmap).mockReturnValue({
      data: undefined,
      isPending: true,
    } as never)
    vi.mocked(useAnalyticsServiceGetRetention).mockReturnValue({
      data: undefined,
      isPending: true,
    } as never)
    vi.mocked(useAnalyticsServiceGetBreakdown).mockReturnValue({
      data: undefined,
      isPending: true,
    } as never)

    const { container } = await renderScreen()

    expect(container.querySelector('[aria-busy="true"]')).toBeInTheDocument()
    expect(screen.getByRole('status', { name: 'Loading stats…' })).toBeInTheDocument()
  })

  it('renders overview KPI cards mapped from GetOverview (reviews, accuracy %, streak days, formatted duration)', async () => {
    const {
      useAnalyticsServiceGetOverview,
      useAnalyticsServiceGetHeatmap,
      useAnalyticsServiceGetRetention,
      useAnalyticsServiceGetBreakdown,
    } = await import('../../lib/api/analytics/analytics-service/analytics-service')
    vi.mocked(useAnalyticsServiceGetOverview).mockReturnValue(populatedOverview as never)
    vi.mocked(useAnalyticsServiceGetHeatmap).mockReturnValue(populatedHeatmap as never)
    vi.mocked(useAnalyticsServiceGetRetention).mockReturnValue(populatedRetention as never)
    vi.mocked(useAnalyticsServiceGetBreakdown).mockReturnValue(populatedBreakdown as never)

    await renderScreen()

    expect(screen.getByText('120')).toBeInTheDocument() // reviews
    expect(screen.getByText('83%')).toBeInTheDocument() // accuracy: 0.833 -> 83
    expect(screen.getByText('5 days')).toBeInTheDocument() // streak
    expect(screen.getByText('2h 15m')).toBeInTheDocument() // 8_100_000ms = 135min = 2h15m
  })

  it('renders the heatmap grid with accessible per-day labels from GetHeatmap.days', async () => {
    const {
      useAnalyticsServiceGetOverview,
      useAnalyticsServiceGetHeatmap,
      useAnalyticsServiceGetRetention,
      useAnalyticsServiceGetBreakdown,
    } = await import('../../lib/api/analytics/analytics-service/analytics-service')
    vi.mocked(useAnalyticsServiceGetOverview).mockReturnValue(populatedOverview as never)
    vi.mocked(useAnalyticsServiceGetHeatmap).mockReturnValue(populatedHeatmap as never)
    vi.mocked(useAnalyticsServiceGetRetention).mockReturnValue(populatedRetention as never)
    vi.mocked(useAnalyticsServiceGetBreakdown).mockReturnValue(populatedBreakdown as never)

    await renderScreen()

    const dayCellOne = screen.getByLabelText('2026-01-01: 3 reviews')
    const dayCellTwo = screen.getByLabelText('2026-01-02: 0 reviews')
    expect(dayCellOne).toBeInTheDocument()
    expect(dayCellTwo).toBeInTheDocument()

    // Regression guard for the ARIA finding: a `role="img"` ancestor would flatten the
    // accessibility tree and make these per-day labels unreachable by real assistive tech,
    // even though getByLabelText (a direct DOM query) would still "pass". Assert there is no
    // role="img" ancestor between each day cell and the document root.
    for (const cell of [dayCellOne, dayCellTwo]) {
      let node: HTMLElement | null = cell
      while (node) {
        expect(node.getAttribute('role')).not.toBe('img')
        node = node.parentElement
      }
    }

    // Every day of the year gets its own accessible label (most defaulting to "0 reviews"
    // for days absent from GetHeatmap.days) — confirms labels are per-cell, distinct,
    // reachable nodes rather than being swallowed by a single aggregate container label.
    expect(screen.getAllByLabelText(/review/i).length).toBeGreaterThan(300)
  })

  it('groups the heatmap by month with short month labels', async () => {
    const {
      useAnalyticsServiceGetOverview,
      useAnalyticsServiceGetHeatmap,
      useAnalyticsServiceGetRetention,
      useAnalyticsServiceGetBreakdown,
    } = await import('../../lib/api/analytics/analytics-service/analytics-service')
    vi.mocked(useAnalyticsServiceGetOverview).mockReturnValue(populatedOverview as never)
    vi.mocked(useAnalyticsServiceGetHeatmap).mockReturnValue(populatedHeatmap as never)
    vi.mocked(useAnalyticsServiceGetRetention).mockReturnValue(populatedRetention as never)
    vi.mocked(useAnalyticsServiceGetBreakdown).mockReturnValue(populatedBreakdown as never)

    await renderScreen()

    // With today pinned to 2026-07-04, the rolling window runs 2025-07-05 → 2026-07-04. Each
    // calendar month in that window is its own labelled block, so both edges are partial "Jul"
    // blocks (Jul 2025 and Jul 2026) — hence two "Jul" labels — while the interior months appear
    // once each. Spot-check the doubled edge month and several distinct interior labels.
    expect(screen.getAllByText('Jul')).toHaveLength(2)
    for (const month of ['Aug', 'Jan', 'Jun']) {
      expect(screen.getByText(month)).toBeInTheDocument()
    }

    // Every day cell still survives the regrouping (no columns dropped at month boundaries).
    expect(screen.getAllByLabelText(/review/i).length).toBeGreaterThan(300)
  })

  it('lays months out as continuous-fill blocks: gap track between months, cursor carried over', async () => {
    const {
      useAnalyticsServiceGetOverview,
      useAnalyticsServiceGetHeatmap,
      useAnalyticsServiceGetRetention,
      useAnalyticsServiceGetBreakdown,
    } = await import('../../lib/api/analytics/analytics-service/analytics-service')
    vi.mocked(useAnalyticsServiceGetOverview).mockReturnValue(populatedOverview as never)
    vi.mocked(useAnalyticsServiceGetHeatmap).mockReturnValue(populatedHeatmap as never)
    vi.mocked(useAnalyticsServiceGetRetention).mockReturnValue(populatedRetention as never)
    vi.mocked(useAnalyticsServiceGetBreakdown).mockReturnValue(populatedBreakdown as never)
    const user = userEvent.setup()

    await renderScreen()

    // Select a full past calendar year (2025, non-leap) so month blocks start at cursor 0.
    await user.selectOptions(screen.getByLabelText('Year'), '2025')

    // Jan block: 31 days fill 5 columns (tracks 1–5) top-to-bottom, 7 per column, on the 7 day
    // rows (1–7); the label sits on the bottom row (8). Jan 1 is the first cell of the first
    // column → row 1.
    const jan1 = screen.getByLabelText('2025-01-01: 0 reviews')
    expect(jan1.style.gridColumn).toBe('1')
    expect(jan1.style.gridRow).toBe('1')

    // Cursor after Jan = 31 % 7 = 3, so Feb resumes on the 4th row (grid row 4) of a fresh
    // column — placed after Jan's 5 columns AND the one empty inter-month gap track → track 7.
    const feb1 = screen.getByLabelText('2025-02-01: 0 reviews')
    expect(feb1.style.gridColumn).toBe('7')
    expect(feb1.style.gridRow).toBe('4')
  })

  it('renders retention measured vs target percentages and due-reviews context', async () => {
    const {
      useAnalyticsServiceGetOverview,
      useAnalyticsServiceGetHeatmap,
      useAnalyticsServiceGetRetention,
      useAnalyticsServiceGetBreakdown,
    } = await import('../../lib/api/analytics/analytics-service/analytics-service')
    vi.mocked(useAnalyticsServiceGetOverview).mockReturnValue(populatedOverview as never)
    vi.mocked(useAnalyticsServiceGetHeatmap).mockReturnValue(populatedHeatmap as never)
    vi.mocked(useAnalyticsServiceGetRetention).mockReturnValue(populatedRetention as never)
    vi.mocked(useAnalyticsServiceGetBreakdown).mockReturnValue(populatedBreakdown as never)

    await renderScreen()

    expect(screen.getByText('Measured: 87%')).toBeInTheDocument()
    expect(screen.getByText('Target: 90%')).toBeInTheDocument()
    expect(screen.getByText('43 of 50 due reviews correct')).toBeInTheDocument()
  })

  it('renders breakdown buckets with key as label and computed accuracy', async () => {
    const {
      useAnalyticsServiceGetOverview,
      useAnalyticsServiceGetHeatmap,
      useAnalyticsServiceGetRetention,
      useAnalyticsServiceGetBreakdown,
    } = await import('../../lib/api/analytics/analytics-service/analytics-service')
    vi.mocked(useAnalyticsServiceGetOverview).mockReturnValue(populatedOverview as never)
    vi.mocked(useAnalyticsServiceGetHeatmap).mockReturnValue(populatedHeatmap as never)
    vi.mocked(useAnalyticsServiceGetRetention).mockReturnValue(populatedRetention as never)
    vi.mocked(useAnalyticsServiceGetBreakdown).mockReturnValue(populatedBreakdown as never)

    await renderScreen()

    expect(screen.getByText('Spanish')).toBeInTheDocument()
    expect(screen.getByText('Japanese')).toBeInTheDocument()
    // Spanish: 70/80 = 87.5% -> rounds to 88
    expect(screen.getByText('80 reviews · 88% correct')).toBeInTheDocument()
    // Japanese: 30/40 = 75%
    expect(screen.getByText('40 reviews · 75% correct')).toBeInTheDocument()
  })

  it('shows the breakdown empty message when there are no buckets', async () => {
    const {
      useAnalyticsServiceGetOverview,
      useAnalyticsServiceGetHeatmap,
      useAnalyticsServiceGetRetention,
      useAnalyticsServiceGetBreakdown,
    } = await import('../../lib/api/analytics/analytics-service/analytics-service')
    vi.mocked(useAnalyticsServiceGetOverview).mockReturnValue(populatedOverview as never)
    vi.mocked(useAnalyticsServiceGetHeatmap).mockReturnValue(populatedHeatmap as never)
    vi.mocked(useAnalyticsServiceGetRetention).mockReturnValue(populatedRetention as never)
    vi.mocked(useAnalyticsServiceGetBreakdown).mockReturnValue({
      data: { data: { buckets: [] } },
      isPending: false,
    } as never)

    await renderScreen()

    expect(screen.getByText('No data for this range.')).toBeInTheDocument()
  })

  it('changing the breakdown-by selector calls the breakdown hook with the new "by" param', async () => {
    const {
      useAnalyticsServiceGetOverview,
      useAnalyticsServiceGetHeatmap,
      useAnalyticsServiceGetRetention,
      useAnalyticsServiceGetBreakdown,
    } = await import('../../lib/api/analytics/analytics-service/analytics-service')
    vi.mocked(useAnalyticsServiceGetOverview).mockReturnValue(populatedOverview as never)
    vi.mocked(useAnalyticsServiceGetHeatmap).mockReturnValue(populatedHeatmap as never)
    vi.mocked(useAnalyticsServiceGetRetention).mockReturnValue(populatedRetention as never)
    vi.mocked(useAnalyticsServiceGetBreakdown).mockReturnValue(populatedBreakdown as never)
    const user = userEvent.setup()

    await renderScreen()

    await user.selectOptions(screen.getByLabelText('Group by'), 'state')

    expect(useAnalyticsServiceGetBreakdown).toHaveBeenLastCalledWith(
      expect.objectContaining({ by: 'state', range: '30d' }),
    )
  })

  it('changing the range selector calls overview/retention/breakdown hooks with the new range', async () => {
    const {
      useAnalyticsServiceGetOverview,
      useAnalyticsServiceGetHeatmap,
      useAnalyticsServiceGetRetention,
      useAnalyticsServiceGetBreakdown,
    } = await import('../../lib/api/analytics/analytics-service/analytics-service')
    vi.mocked(useAnalyticsServiceGetOverview).mockReturnValue(populatedOverview as never)
    vi.mocked(useAnalyticsServiceGetHeatmap).mockReturnValue(populatedHeatmap as never)
    vi.mocked(useAnalyticsServiceGetRetention).mockReturnValue(populatedRetention as never)
    vi.mocked(useAnalyticsServiceGetBreakdown).mockReturnValue(populatedBreakdown as never)
    const user = userEvent.setup()

    await renderScreen()

    await user.selectOptions(screen.getByLabelText('Range'), '7')

    expect(useAnalyticsServiceGetOverview).toHaveBeenLastCalledWith(
      expect.objectContaining({ range: '7d' }),
    )
    expect(useAnalyticsServiceGetRetention).toHaveBeenLastCalledWith(
      expect.objectContaining({ range: '7d' }),
    )
    expect(useAnalyticsServiceGetBreakdown).toHaveBeenLastCalledWith(
      expect.objectContaining({ by: 'deck', range: '7d' }),
    )
  })

  it('changing the year selector calls the heatmap hook with the new year', async () => {
    const {
      useAnalyticsServiceGetOverview,
      useAnalyticsServiceGetHeatmap,
      useAnalyticsServiceGetRetention,
      useAnalyticsServiceGetBreakdown,
    } = await import('../../lib/api/analytics/analytics-service/analytics-service')
    vi.mocked(useAnalyticsServiceGetOverview).mockReturnValue(populatedOverview as never)
    vi.mocked(useAnalyticsServiceGetHeatmap).mockReturnValue(populatedHeatmap as never)
    vi.mocked(useAnalyticsServiceGetRetention).mockReturnValue(populatedRetention as never)
    vi.mocked(useAnalyticsServiceGetBreakdown).mockReturnValue(populatedBreakdown as never)
    const user = userEvent.setup()

    await renderScreen()

    // A specific past calendar year (distinct from the Current window's own previous-year
    // fetch), so the assertion reflects the user's selection rather than the initial render.
    const targetYear = new Date().getUTCFullYear() - 2
    await user.selectOptions(screen.getByLabelText('Year'), String(targetYear))

    expect(useAnalyticsServiceGetHeatmap).toHaveBeenCalledWith(
      expect.objectContaining({ year: targetYear }),
    )
  })

  it('year selector offers every year from the registration year through last year', async () => {
    const {
      useAnalyticsServiceGetOverview,
      useAnalyticsServiceGetHeatmap,
      useAnalyticsServiceGetRetention,
      useAnalyticsServiceGetBreakdown,
    } = await import('../../lib/api/analytics/analytics-service/analytics-service')
    vi.mocked(useAnalyticsServiceGetOverview).mockReturnValue(populatedOverview as never)
    vi.mocked(useAnalyticsServiceGetHeatmap).mockReturnValue(populatedHeatmap as never)
    vi.mocked(useAnalyticsServiceGetRetention).mockReturnValue(populatedRetention as never)
    vi.mocked(useAnalyticsServiceGetBreakdown).mockReturnValue(populatedBreakdown as never)

    // renderScreen() seeds a 2020-01-01 registration; today is pinned to 2026. The selector
    // should list "Current" plus every full past year 2025 → 2020 (registration year included),
    // and nothing before registration.
    await renderScreen()

    const yearSelect = screen.getByLabelText('Year') as HTMLSelectElement
    const optionValues = Array.from(yearSelect.options).map((o) => o.value)
    expect(optionValues).toEqual(['current', '2025', '2024', '2023', '2022', '2021', '2020'])
  })

  it('rolling window endpoints share the same date number (leap-year independent)', async () => {
    const {
      useAnalyticsServiceGetOverview,
      useAnalyticsServiceGetHeatmap,
      useAnalyticsServiceGetRetention,
      useAnalyticsServiceGetBreakdown,
    } = await import('../../lib/api/analytics/analytics-service/analytics-service')
    vi.mocked(useAnalyticsServiceGetOverview).mockReturnValue(populatedOverview as never)
    vi.mocked(useAnalyticsServiceGetHeatmap).mockReturnValue(populatedHeatmap as never)
    vi.mocked(useAnalyticsServiceGetRetention).mockReturnValue(populatedRetention as never)
    vi.mocked(useAnalyticsServiceGetBreakdown).mockReturnValue(populatedBreakdown as never)

    // Today is pinned to 2026-07-04, so the "Current" window must run exactly 2025-07-04 →
    // 2026-07-04 — same day-of-month at both ends, never off by one (the old fixed-364-day
    // window produced 2025-07-05 → 2026-07-04).
    await renderScreen()

    expect(screen.getByLabelText('2025-07-04: 0 reviews')).toBeInTheDocument()
    expect(screen.getByLabelText('2026-07-04: 0 reviews')).toBeInTheDocument()
    expect(screen.queryByLabelText('2025-07-03: 0 reviews')).not.toBeInTheDocument()
  })
})
