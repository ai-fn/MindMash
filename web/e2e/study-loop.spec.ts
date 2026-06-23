import { expect, test } from '@playwright/test'

// Full golden-path E2E (Task 9, see task-9-brief.md): login -> create deck -> quick-add a
// Basic note -> study -> reveal -> grade -> empty/deck-complete state. Runs against a real
// backend (no mocks) — see web/README.md's new "End-to-end tests" section for how to start
// the four Go services before `npm run e2e`.
//
// There is no UI registration screen in this MVP slice, so each run registers a fresh
// throwaway user directly via the API (judgment call already made in the brief — keeps the
// test server-authoritative and idempotent across reruns without inventing a seed script or
// a UI that doesn't exist).
const TEST_PASSWORD = 'correct-horse-battery-staple'

test('login, create a deck, quick-add a note, study it, and grade to deck-complete', async ({
  page,
  request,
}) => {
  const email = `e2e-${Date.now()}@example.com`

  const registerResponse = await request.post('/v1/auth/register', {
    data: { email, password: TEST_PASSWORD, locale: 'en' },
  })
  expect(
    registerResponse.ok(),
    `POST /v1/auth/register failed with ${registerResponse.status()} — is the identity service running? See web/README.md's "End-to-end tests" section.`,
  ).toBe(true)

  // --- Login ---
  await page.goto('/login')
  await page.getByLabel('Email').fill(email)
  await page.getByLabel('Password').fill(TEST_PASSWORD)
  await page.getByRole('button', { name: 'Log in', exact: true }).click()
  await page.waitForURL('/')

  // --- Create a deck ---
  const deckName = `E2E Deck ${Date.now()}`
  await page.getByLabel('New deck name').fill(deckName)
  await page.getByRole('button', { name: 'Create deck' }).click()
  const deckLink = page.getByRole('link', { name: new RegExp(deckName) })
  await expect(deckLink).toBeVisible({ timeout: 15_000 })

  // --- Quick-add a Basic note onto that deck ---
  const deckListItem = page.locator('li').filter({ has: deckLink })
  await deckListItem.getByRole('button', { name: 'Add card' }).click()
  await page.getByLabel('Front').fill('What is the capital of France?')
  await page.getByLabel('Back').fill('Paris')
  // Disabled until the Basic note type resolves (useBasicNoteType may need to create it
  // on a fresh account) — wait on the button's enabled state, not a fixed timeout.
  const addNoteButton = page.getByRole('button', { name: 'Add note' })
  await expect(addNoteButton).toBeEnabled({ timeout: 15_000 })
  await addNoteButton.click()
  // Form clearing is the proof of a successful submit.
  await expect(page.getByLabel('Front')).toHaveValue('', { timeout: 15_000 })

  // --- Study the deck ---
  const deckHref = await deckLink.getAttribute('href')
  expect(deckHref).toBeTruthy()
  await deckLink.click()
  await page.waitForURL(`**${deckHref}`)

  await page.getByRole('button', { name: 'Show answer' }).click({ timeout: 15_000 })
  const gradeButtons = page.getByRole('button', { name: /Again|Hard|Good|Easy/ })
  await expect(gradeButtons.first()).toBeVisible({ timeout: 15_000 })
  await page.getByRole('button', { name: /^Good/ }).click()

  // --- Deck complete (1-card deck) ---
  const backToDecks = page.getByRole('link', { name: 'Back to decks' })
  await expect(backToDecks).toBeVisible({ timeout: 15_000 })
  await backToDecks.click()
  await page.waitForURL('/')
})
