import { describe, expect, it } from 'vitest'

import { parseCardMessage, isFromExpectedSource, CARD_MESSAGE_TYPES } from './cardBridge.ts'

describe('parseCardMessage', () => {
  it.each(CARD_MESSAGE_TYPES)('parses a valid %s message', (type) => {
    const result = parseCardMessage({ type })
    expect(result).toEqual({ type })
  })

  it('preserves extra payload fields alongside a valid type', () => {
    const result = parseCardMessage({ type: 'grade', score: 3, foo: 'bar' })
    expect(result).toEqual({ type: 'grade', score: 3, foo: 'bar' })
  })

  it.each([
    ['null', null],
    ['undefined', undefined],
    ['a string', 'reveal'],
    ['an array', ['reveal']],
    ['an empty object', {}],
    ['a non-string type', { type: 123 }],
    ['an unknown type literal', { type: 'eval' }],
    ['a number', 42],
    ['a boolean', true],
  ])('returns null for %s', (_label, data) => {
    expect(parseCardMessage(data)).toBeNull()
  })
})

describe('isFromExpectedSource', () => {
  it('returns true when event.source matches a non-null expected source', () => {
    const source = {} as Window
    const event = { source } as MessageEvent
    expect(isFromExpectedSource(event, source)).toBe(true)
  })

  it('returns false for a different source', () => {
    const expected = {} as Window
    const other = {} as Window
    const event = { source: other } as MessageEvent
    expect(isFromExpectedSource(event, expected)).toBe(false)
  })

  it('returns false when expected source is null', () => {
    const event = { source: {} as Window } as MessageEvent
    expect(isFromExpectedSource(event, null)).toBe(false)
  })

  it('returns false when event.source is null', () => {
    const expected = {} as Window
    const event = { source: null } as MessageEvent
    expect(isFromExpectedSource(event, expected)).toBe(false)
  })

  it('returns false when both event.source and expected source are null', () => {
    const event = { source: null } as MessageEvent
    expect(isFromExpectedSource(event, null)).toBe(false)
  })
})
