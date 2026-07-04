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
  })

  afterEach(() => {
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

    expect(useAnalyticsServiceGetBreakdown).toHaveBeenLastCalledWith({ by: 'state', range: '30d' })
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

    expect(useAnalyticsServiceGetOverview).toHaveBeenLastCalledWith({ range: '7d' })
    expect(useAnalyticsServiceGetRetention).toHaveBeenLastCalledWith({ range: '7d' })
    expect(useAnalyticsServiceGetBreakdown).toHaveBeenLastCalledWith({ by: 'deck', range: '7d' })
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

    const previousYear = String(new Date().getFullYear() - 1)
    await user.selectOptions(screen.getByLabelText('Year'), previousYear)

    expect(useAnalyticsServiceGetHeatmap).toHaveBeenLastCalledWith({ year: Number(previousYear) })
  })
})
