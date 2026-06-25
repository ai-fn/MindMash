import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { IconButton, MoreIcon, cn } from '../../ui'

export interface DeckMenuProps {
  deckName: string
  onAddCard: () => void
}

// Per-deck overflow menu (Task 5, see task-5-brief.md). Deliberately offers only
// "Add card" as a real action — "Study" is omitted even though it would be a natural
// fit for a deck-actions menu, because the deck row itself is already the Study
// affordance (clicking the row navigates to /study/{deckId}). Constraint 11 ("same
// action → same position") argues against adding a second control that does the
// same thing in a second place; it would invite "which one do I use" confusion for
// no benefit, since the row-click is already the lowest-friction way to start
// studying. "Rename"/"Delete" are visibly present but disabled — Task 11 wires the
// actual deck-mutation RPCs; until then they're inert placeholders rather than
// silently missing, so users can see they're coming rather than wonder if the
// product can't do it.
export function DeckMenu({ deckName, onAddCard }: DeckMenuProps) {
  const { t } = useTranslation('decks')
  const [open, setOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) {
      return
    }

    function handlePointerDown(event: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        setOpen(false)
      }
    }

    function handleKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') {
        setOpen(false)
      }
    }

    document.addEventListener('mousedown', handlePointerDown)
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('mousedown', handlePointerDown)
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [open])

  return (
    <div ref={containerRef} className="relative">
      <IconButton
        label={t('menu.label', { deckName })}
        icon={<MoreIcon />}
        size="sm"
        aria-expanded={open}
        aria-haspopup="menu"
        onClick={() => setOpen((value) => !value)}
      />
      {open ? (
        <div
          role="menu"
          aria-label={t('menu.label', { deckName })}
          className="absolute right-0 top-full z-10 mt-1 w-44 rounded-[var(--radius-md)] border border-border bg-surface p-1 shadow-lg"
        >
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              setOpen(false)
              onAddCard()
            }}
            className="w-full rounded-[var(--radius-sm)] px-3 py-1.5 text-left text-sm text-fg hover:bg-accent-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
          >
            {t('menu.addCard')}
          </button>
          <button
            type="button"
            role="menuitem"
            disabled
            className={cn(
              'w-full rounded-[var(--radius-sm)] px-3 py-1.5 text-left text-sm text-muted',
              'cursor-not-allowed opacity-60',
            )}
          >
            {t('menu.rename')}
          </button>
          <button
            type="button"
            role="menuitem"
            disabled
            className={cn(
              'w-full rounded-[var(--radius-sm)] px-3 py-1.5 text-left text-sm text-muted',
              'cursor-not-allowed opacity-60',
            )}
          >
            {t('menu.delete')}
          </button>
        </div>
      ) : null}
    </div>
  )
}
