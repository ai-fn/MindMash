import { expect, test } from '@playwright/test'

import { createDeckWithNote, loginViaUi, registerFreshUser } from './helpers'

// Stats (analytics) + Browse (search) are backed by the analytics and search services
// (Tasks 7-9), reached via the Vite dev proxy's /v1/stats and /v1/search rules (see
// vite.config.ts). This asserts the integration is actually reachable end-to-end (a 2xx from
// each backend) while tolerating empty data: the analytics event pipeline and the search
// index are asynchronous, so a just-created account may legitimately have no stats/results
// yet — the screen must still load and settle into its dashboard/empty (not error) state.
test('stats and browse screens reach their backends and render', async ({ page, request }) => {
  const email = await registerFreshUser(request)
  await loginViaUi(page, email)
  await createDeckWithNote(page)

  // --- Stats ---
  const overviewResponse = page.waitForResponse(
    (response) =>
      response.url().includes('/v1/stats/overview') && response.request().method() === 'GET',
  )
  await page.getByRole('link', { name: 'Stats' }).click()
  await page.waitForURL('**/stats')
  expect((await overviewResponse).ok()).toBe(true)

  await expect(page.getByRole('heading', { name: 'Your stats' })).toBeVisible()
  // Settles into either the populated dashboard (the "Reviews" KPI) or the invitational empty
  // state — both are valid depending on how far the analytics pipeline has caught up.
  await expect(
    page.getByText('Reviews', { exact: true }).or(page.getByText('No stats yet')).first(),
  ).toBeVisible({ timeout: 15_000 })

  // --- Browse ---
  const searchResponse = page.waitForResponse((response) => response.url().includes('/v1/search'))
  await page.getByRole('link', { name: 'Browse' }).click()
  await page.waitForURL('**/browse')
  expect((await searchResponse).ok()).toBe(true)

  await expect(page.getByRole('heading', { name: 'Browse cards' })).toBeVisible()
  await expect(page.getByLabel('Search')).toBeVisible()
  // Settles into results (at least one card) or the empty state — never the error state.
  await expect(
    page.getByRole('button', { name: 'Card result' }).or(page.getByText('No cards match.')).first(),
  ).toBeVisible({ timeout: 15_000 })
})

// Settings exposes the two user-facing preferences (Task 12): theme (light/dark/system) and
// the "advanced grading" toggle. Advanced grading is the FSRS reconciliation seam (Task 6):
// flipping it on must switch the study screen from the default 2-button UI to the full
// 4-button Again/Hard/Good/Easy UI. This drives both end to end.
test('settings: theme applies and advanced grading drives the study UI', async ({
  page,
  request,
}) => {
  const email = await registerFreshUser(request)
  await loginViaUi(page, email)
  const { deckName, deckHref } = await createDeckWithNote(page)

  await page.getByRole('link', { name: 'Settings' }).click()
  await page.waitForURL('**/settings')

  // Theme: cycle to Dark and assert the <html data-theme> the CSS keys off actually updates.
  // Both the app-shell header and the Appearance section render a ThemeToggle backed by the
  // same store; either drives the global theme, so target the first and assert the DOM effect.
  const html = page.locator('html')
  const themeToggle = page.getByRole('button', { name: /Current theme/ }).first()
  for (let i = 0; i < 3 && (await html.getAttribute('data-theme')) !== 'dark'; i += 1) {
    await themeToggle.click()
  }
  await expect(html).toHaveAttribute('data-theme', 'dark')

  // Advanced grading: off by default, flip it on.
  const advancedGrading = page.getByRole('switch', { name: /Advanced grading/ })
  await expect(advancedGrading).not.toBeChecked()
  await advancedGrading.click()
  await expect(advancedGrading).toBeChecked()

  // Now study the deck: the just-set preference must yield the 4-button grade UI.
  await page.getByRole('link', { name: 'Decks' }).click()
  await page.waitForURL('/')
  await page.getByRole('link', { name: new RegExp(deckName) }).click()
  await page.waitForURL(`**${deckHref}`)

  await page.getByRole('button', { name: 'Show answer' }).click({ timeout: 15_000 })
  // All four FSRS grades present (advanced mode), not the 2-button default.
  for (const grade of ['Again', 'Hard', 'Good', 'Easy']) {
    await expect(page.getByRole('button', { name: new RegExp(`^${grade}`) })).toBeVisible({
      timeout: 15_000,
    })
  }
  await page.getByRole('button', { name: /^Good/ }).click()

  await expect(page.getByRole('link', { name: 'Back to decks' })).toBeVisible({ timeout: 15_000 })
})
