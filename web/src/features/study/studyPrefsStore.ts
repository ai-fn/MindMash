// Zustand study-prefs store: persists the "advanced grading" setting (Task 12's
// Settings screen will add a toggle UI for it; this task only needs the store and
// StudyScreen's read of it). Follows the exact pattern of
// features/theme/themeStore.ts — a plain create() with persistence side effects
// performed directly in the setter, no external persistence middleware — so the two
// stores stay consistent for anyone reading either as a reference.
import { create } from 'zustand'

const STORAGE_KEY = 'mindmesh.study.advancedGrading'

interface StudyPrefsState {
  /**
   * When false (default), StudyScreen shows only 2 grade buttons (Don't Know / Know).
   * When true, it shows all 4 FSRS grade buttons (Again/Hard/Good/Easy) with their
   * GetPreview interval labels. See task-6-brief.md's FSRS grade reconciliation note.
   */
  advancedGrading: boolean
  setAdvancedGrading: (value: boolean) => void
  toggle: () => void
}

function readPersisted(): boolean {
  if (typeof window === 'undefined' || !window.localStorage) {
    return false
  }
  return window.localStorage.getItem(STORAGE_KEY) === 'true'
}

function persist(value: boolean): void {
  if (typeof window === 'undefined' || !window.localStorage) {
    return
  }
  window.localStorage.setItem(STORAGE_KEY, String(value))
}

export const useStudyPrefsStore = create<StudyPrefsState>((set, get) => ({
  advancedGrading: readPersisted(),

  setAdvancedGrading(value: boolean) {
    persist(value)
    set({ advancedGrading: value })
  },

  toggle() {
    get().setAdvancedGrading(!get().advancedGrading)
  },
}))
