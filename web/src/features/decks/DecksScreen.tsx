import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'

import { useAuthStore } from '../auth/authStore'
import { useDeckServiceListDecks } from '../../lib/api/collection/deck-service/deck-service'
import { DeckRow } from './DeckRow'

// Home / deck-picker (Task 4, see task-4-brief.md). Replaces the Task 3 placeholder
// HomeScreen: lists the user's decks with per-deck study counts, each linking to
// /study/{deckId} (Task 6's route — not stubbed here, see brief judgment call 4).
export function DecksScreen() {
  const { t } = useTranslation('decks')
  const logout = useAuthStore((state) => state.logout)
  const { data, isPending } = useDeckServiceListDecks()

  const items = data?.data.items ?? []

  return (
    <main className="flex min-h-screen flex-col gap-6 bg-slate-50 p-6">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold text-slate-800">{t('title')}</h1>
        <button
          type="button"
          onClick={() => void logout()}
          className="rounded bg-slate-800 px-4 py-2 text-white"
        >
          {t('logout')}
        </button>
      </div>

      {isPending ? (
        <p className="text-slate-600">{t('loading')}</p>
      ) : items.length === 0 ? (
        // Task 7 (minimal authoring / create-deck) will add a create-deck action
        // here; out of scope for this task (see task-4-brief.md judgment call 3).
        <p className="text-slate-600">{t('empty')}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {items.map((deck) => (
            <li key={deck.id}>
              <Link to={`/study/${deck.id}`}>
                <DeckRow deckId={deck.id ?? ''} name={deck.name ?? ''} />
              </Link>
            </li>
          ))}
        </ul>
      )}
    </main>
  )
}
