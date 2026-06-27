import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { IconButton, MoreIcon } from '../../ui'

export interface DeckMenuProps {
  deckName: string
  onAddCard: () => void
  /** Opens the rename dialog for this deck (Task 11a). */
  onRename: () => void
  /** Opens the delete-confirm dialog for this deck (Task 11a). */
  onDelete: () => void
  /** Opens the create-subdeck dialog, parented to this deck (Task 11a). */
  onCreateSubdeck: () => void
  /** Opens the deck-config (scheduling settings) dialog for this deck (Task 11a). */
  onEditConfig: () => void
}

// Per-deck overflow menu (Task 5, see task-5-brief.md). Originally offered only
// "Add card" as a real action, with "Rename"/"Delete" visibly present but disabled —
// Task 11a now wires those up for real and adds "Create subdeck" / "Deck settings"
// (config editing). "Study" is still omitted even though it would be a natural fit for
// a deck-actions menu, because the deck row itself is already the Study affordance
// (clicking the row navigates to /study/{deckId}) — Constraint 11 ("same action → same
// position") argues against a second control doing the same thing in a second place.
//
// Judgment call (Task 11a): all four real actions just call back out to parent-owned
// `onX` callbacks rather than owning any dialogs themselves. DeckMenu is a transient,
// click-outside-closing popup (see the outside-click/Escape handling below) — a
// confirm-delete or a multi-field config form needs to survive the menu closing, so
// those dialogs live as DecksScreen-level siblings (mirroring the existing
// NewDeck/QuickAddNote convention) rather than nested inside this popup.
export function DeckMenu({
  deckName,
  onAddCard,
  onRename,
  onDelete,
  onCreateSubdeck,
  onEditConfig,
}: DeckMenuProps) {
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

  function handleSelect(action: () => void) {
    setOpen(false)
    action()
  }

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
          className="absolute right-0 top-full z-10 mt-1 w-48 rounded-[var(--radius-md)] border border-border bg-surface p-1 shadow-lg"
        >
          <button
            type="button"
            role="menuitem"
            onClick={() => handleSelect(onAddCard)}
            className="w-full rounded-[var(--radius-sm)] px-3 py-1.5 text-left text-sm text-fg hover:bg-accent-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
          >
            {t('menu.addCard')}
          </button>
          <button
            type="button"
            role="menuitem"
            onClick={() => handleSelect(onCreateSubdeck)}
            className="w-full rounded-[var(--radius-sm)] px-3 py-1.5 text-left text-sm text-fg hover:bg-accent-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
          >
            {t('menu.createSubdeck')}
          </button>
          <button
            type="button"
            role="menuitem"
            onClick={() => handleSelect(onEditConfig)}
            className="w-full rounded-[var(--radius-sm)] px-3 py-1.5 text-left text-sm text-fg hover:bg-accent-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
          >
            {t('menu.editConfig')}
          </button>
          <button
            type="button"
            role="menuitem"
            onClick={() => handleSelect(onRename)}
            className="w-full rounded-[var(--radius-sm)] px-3 py-1.5 text-left text-sm text-fg hover:bg-accent-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
          >
            {t('menu.rename')}
          </button>
          <button
            type="button"
            role="menuitem"
            onClick={() => handleSelect(onDelete)}
            className="w-full rounded-[var(--radius-sm)] px-3 py-1.5 text-left text-sm text-danger hover:bg-accent-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
          >
            {t('menu.delete')}
          </button>
        </div>
      ) : null}
    </div>
  )
}
