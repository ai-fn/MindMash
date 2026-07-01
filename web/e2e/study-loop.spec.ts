import { expect, test } from '@playwright/test'

import { createDeckWithNote, loginViaUi, registerFreshUser } from './helpers'

// Core study loop (redesigned in Task 6): login -> create deck -> quick-add a Basic note ->
// study -> reveal -> grade -> deck-complete, against a real backend (no mocks). See
// web/README.md's "End-to-end tests" section for how to start the services first.
//
// This exercises the DEFAULT 2-button grading UI ("Don't Know" -> GRADE_AGAIN,
// "Know" -> GRADE_GOOD). The 4-button advanced grading path is covered in
// browse-stats-settings.spec.ts, which flips the setting on first.
test('study a deck: reveal and grade Know to deck-complete', async ({ page, request }) => {
  const email = await registerFreshUser(request)
  await loginViaUi(page, email)

  const { deckName, deckHref } = await createDeckWithNote(page)

  // --- Study the deck ---
  await page.getByRole('link', { name: new RegExp(deckName) }).click()
  await page.waitForURL(`**${deckHref}`)

  await page.getByRole('button', { name: 'Show answer' }).click({ timeout: 15_000 })

  // Both grade buttons are always present (Constraint 5). `exact` on "Know" so it doesn't also
  // match "Don't Know". Grade "Know" (~GRADE_GOOD).
  await expect(page.getByRole('button', { name: "Don't Know", exact: true })).toBeVisible({
    timeout: 15_000,
  })
  await page.getByRole('button', { name: 'Know', exact: true }).click()

  // --- Deck complete (1-card deck) ---
  const backToDecks = page.getByRole('link', { name: 'Back to decks' })
  await expect(backToDecks).toBeVisible({ timeout: 15_000 })
  await backToDecks.click()
  await page.waitForURL('/')
})
