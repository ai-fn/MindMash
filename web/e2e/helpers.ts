import { expect, type APIRequestContext, type Page } from '@playwright/test'

// Shared E2E helpers for the redesigned happy path. Every spec registers its own fresh
// throwaway user (there's no UI registration screen in this MVP slice), logs in through the
// real UI, and builds a one-card deck to study. Kept here so the register/login/seed
// boilerplate isn't copy-pasted across specs. All flows run against a real backend (no
// mocks) — see web/README.md's "End-to-end tests" section for the services to start first.

export const TEST_PASSWORD = 'correct-horse-battery-staple'

// Registers a fresh user directly via the identity API and returns its email.
//
// Playwright's webServer only waits for the Vite dev server to answer `/`; its proxy
// middleware to the backend can still be wiring up for a moment after that, so the very first
// proxied request right at test start can land in that gap. Retry briefly rather than failing
// the whole run on a one-time dev-server cold-start race.
export async function registerFreshUser(request: APIRequestContext): Promise<string> {
  const email = `e2e-${Date.now()}-${Math.random().toString(36).slice(2, 7)}@example.com`
  const body = { email, password: TEST_PASSWORD, locale: 'en' }

  let response = await request.post('/v1/auth/register', { data: body })
  for (let attempt = 0; !response.ok() && attempt < 5; attempt += 1) {
    await new Promise((resolve) => setTimeout(resolve, 1000))
    response = await request.post('/v1/auth/register', { data: body })
  }
  expect(
    response.ok(),
    `POST /v1/auth/register failed with ${response.status()} — are the identity/collection/` +
      `scheduler/sync/analytics/search services running? See web/README.md's "End-to-end ` +
      `tests" section.`,
  ).toBe(true)

  return email
}

// Logs the given user in through the real login screen and waits for the decks home.
export async function loginViaUi(page: Page, email: string): Promise<void> {
  await page.goto('/login')
  await page.getByLabel('Email').fill(email)
  await page.getByLabel('Password').fill(TEST_PASSWORD)
  // `exact` so this doesn't also match the "Logging in…" pending label.
  await page.getByRole('button', { name: 'Log in', exact: true }).click()
  await page.waitForURL('/')
}

// Creates a deck (via the header "Create deck" dialog) and quick-adds one Basic note to it
// (via the per-deck overflow menu → "Add card"). Returns the deck's display name and its
// /study/:deckId href. Assumes the caller is on the decks home ('/').
export async function createDeckWithNote(
  page: Page,
): Promise<{ deckName: string; deckHref: string }> {
  const deckName = `E2E Deck ${Date.now()}-${Math.random().toString(36).slice(2, 7)}`

  // A fresh account renders a "Create deck" button in both the header and the empty state, so
  // scope the opener to the first match (the header) and the submit to the open dialog.
  await page.getByRole('button', { name: 'Create deck' }).first().click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('New deck name').fill(deckName)
  await dialog.getByRole('button', { name: 'Create deck' }).click()

  const deckLink = page.getByRole('link', { name: new RegExp(deckName) })
  await expect(deckLink).toBeVisible({ timeout: 15_000 })
  const deckHref = await deckLink.getAttribute('href')
  expect(deckHref).toBeTruthy()

  // Quick-add a Basic note through the redesigned per-deck overflow menu.
  await page.getByRole('button', { name: `More actions for ${deckName}` }).click()
  await page.getByRole('menuitem', { name: 'Add card' }).click()
  await page.getByLabel('Front').fill('What is the capital of France?')
  await page.getByLabel('Back').fill('Paris')
  // Disabled until the Basic note type resolves (useBasicNoteType may need to create it on a
  // fresh account) — wait on the button's enabled state, not a fixed timeout.
  const addNote = page.getByRole('button', { name: 'Add note' })
  await expect(addNote).toBeEnabled({ timeout: 15_000 })
  await addNote.click()
  // A cleared Front field is the proof of a successful submit.
  await expect(page.getByLabel('Front')).toHaveValue('', { timeout: 15_000 })

  return { deckName, deckHref: deckHref ?? '' }
}
