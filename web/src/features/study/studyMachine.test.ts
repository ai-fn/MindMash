import { describe, expect, it } from 'vitest'

import {
  LOADING,
  advance,
  initMachine,
  reveal,
  type StudyCard,
  type StudyMachineState,
} from './studyMachine.ts'

const card = (cardId: string): StudyCard => ({
  cardId,
  deckId: 'deck-1',
  frontHtml: `<p>${cardId}</p>`,
})

describe('initMachine', () => {
  it('returns empty for an empty queue', () => {
    expect(initMachine([])).toEqual({ status: 'empty' })
  })

  it('returns showingFront at cursor 0 with the queue for a non-empty queue', () => {
    const queue = [card('a')]
    expect(initMachine(queue)).toEqual({ status: 'showingFront', queue, cursor: 0 })
  })
})

describe('reveal', () => {
  it('transitions showingFront -> showingBack, keeping queue/cursor unchanged', () => {
    const queue = [card('a'), card('b')]
    const state: StudyMachineState = { status: 'showingFront', queue, cursor: 1 }
    expect(reveal(state)).toEqual({ status: 'showingBack', queue, cursor: 1 })
  })

  it.each<[string, StudyMachineState]>([
    ['loading', LOADING],
    ['empty', { status: 'empty' }],
    ['done', { status: 'done' }],
    ['already showingBack', { status: 'showingBack', queue: [card('a')], cursor: 0 }],
  ])('is a no-op from %s', (_label, state) => {
    expect(reveal(state)).toEqual(state)
  })
})

describe('advance', () => {
  it('transitions showingBack -> showingFront at cursor+1 when more cards remain', () => {
    const queue = [card('a'), card('b'), card('c')]
    const state: StudyMachineState = { status: 'showingBack', queue, cursor: 0 }
    expect(advance(state)).toEqual({ status: 'showingFront', queue, cursor: 1 })
  })

  it('transitions showingBack -> done on the last card', () => {
    const queue = [card('a'), card('b')]
    const state: StudyMachineState = { status: 'showingBack', queue, cursor: 1 }
    expect(advance(state)).toEqual({ status: 'done' })
  })

  it.each<[string, StudyMachineState]>([
    ['loading', LOADING],
    ['empty', { status: 'empty' }],
    ['done', { status: 'done' }],
    ['showingFront', { status: 'showingFront', queue: [card('a')], cursor: 0 }],
  ])('is a no-op from %s', (_label, state) => {
    expect(advance(state)).toEqual(state)
  })
})

describe('full walk through a 3-card queue', () => {
  it('drives init -> reveal -> advance through every card to done', () => {
    const queue = [card('a'), card('b'), card('c')]

    let state = initMachine(queue)
    expect(state).toEqual({ status: 'showingFront', queue, cursor: 0 })

    state = reveal(state)
    expect(state).toEqual({ status: 'showingBack', queue, cursor: 0 })

    state = advance(state)
    expect(state).toEqual({ status: 'showingFront', queue, cursor: 1 })

    state = reveal(state)
    expect(state).toEqual({ status: 'showingBack', queue, cursor: 1 })

    state = advance(state)
    expect(state).toEqual({ status: 'showingFront', queue, cursor: 2 })

    state = reveal(state)
    expect(state).toEqual({ status: 'showingBack', queue, cursor: 2 })

    state = advance(state)
    expect(state).toEqual({ status: 'done' })
  })
})
