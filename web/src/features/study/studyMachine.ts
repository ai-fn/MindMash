// Pure study-loop state machine — no React, no DOM, no network/API imports.
//
// This module only knows about the shape of the study queue and the four
// transitions below. It is deliberately ignorant of grades, the answer
// submission API, and IntervalPreview rendering: Task 6b (StudyScreen.tsx)
// owns the network call and calls `advance()` once that call resolves. Keeping
// the machine framework-free makes it trivially unit-testable without mounting
// any component or mocking any network hook, and reusable even if the
// backend's answer contract changes.

export type StudyCard = {
  cardId: string
  deckId: string
  frontHtml: string
}

export type StudyMachineState =
  | { status: 'loading' }
  | { status: 'empty' }
  | { status: 'showingFront'; queue: StudyCard[]; cursor: number }
  | { status: 'showingBack'; queue: StudyCard[]; cursor: number }
  | { status: 'done' }

/**
 * The machine itself never produces `loading` — it's a state Task 6b sets
 * while the initial queue fetch is in flight, before `initMachine` is ever
 * called. Exported as a shared constant for Task 6b's initial `useState`.
 */
export const LOADING: StudyMachineState = { status: 'loading' }

export function initMachine(queue: StudyCard[]): StudyMachineState {
  if (queue.length === 0) {
    return { status: 'empty' }
  }
  return { status: 'showingFront', queue, cursor: 0 }
}

/**
 * Valid only from `showingFront` → `showingBack` (queue/cursor unchanged).
 * Called from any other status, returns `state` unchanged — out-of-order
 * calls are safe no-ops, never a thrown error. That invariant is what lets
 * Task 6b's screen call this from keyboard-shortcut handlers without first
 * having to prove the current status to the type checker.
 */
export function reveal(state: StudyMachineState): StudyMachineState {
  if (state.status !== 'showingFront') {
    return state
  }
  return { status: 'showingBack', queue: state.queue, cursor: state.cursor }
}

/**
 * Valid only from `showingBack`. Moves to the next card's `showingFront` if
 * one remains in the queue, otherwise to `done`. Called from any other
 * status, returns `state` unchanged (same no-op rule as `reveal`).
 */
export function advance(state: StudyMachineState): StudyMachineState {
  if (state.status !== 'showingBack') {
    return state
  }
  const nextCursor = state.cursor + 1
  if (nextCursor < state.queue.length) {
    return { status: 'showingFront', queue: state.queue, cursor: nextCursor }
  }
  return { status: 'done' }
}
