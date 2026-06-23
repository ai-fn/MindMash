// Pure message-validation logic for the CardFrame sandbox bridge.
//
// This module is intentionally framework-agnostic and DOM-free (aside from the
// `MessageEvent`/`Window` types it validates against) so it can be unit-tested in
// isolation from any iframe/React lifecycle. See CardFrame.tsx for how these helpers
// gate which postMessage events from the sandboxed card iframe are trusted.

export const CARD_MESSAGE_TYPES = ['reveal', 'grade', 'playAudio', 'hint'] as const

export type CardMessageType = (typeof CARD_MESSAGE_TYPES)[number]

export type CardMessage = { type: CardMessageType } & Record<string, unknown>

function isCardMessageType(value: unknown): value is CardMessageType {
  return typeof value === 'string' && (CARD_MESSAGE_TYPES as readonly string[]).includes(value)
}

/**
 * Shape validation only — does not know about MessageEvent/Window. Returns the data
 * cast as CardMessage when it is a plain object with a `type` field equal to one of
 * the four known literal strings; otherwise returns null. All fields other than
 * `type` pass through unvalidated — it is the consumer's responsibility (Task 6) to
 * further validate payload fields it actually uses.
 */
export function parseCardMessage(data: unknown): CardMessage | null {
  if (typeof data !== 'object' || data === null || Array.isArray(data)) {
    return null
  }
  const candidate = data as Record<string, unknown>
  if (!isCardMessageType(candidate.type)) {
    return null
  }
  return candidate as CardMessage
}

/**
 * The actual security check gating message dispatch. This is NOT origin-based: a
 * `srcdoc` + `sandbox="allow-scripts"` iframe (deliberately without
 * `allow-same-origin`) reports `event.origin === 'null'` for every such frame, so
 * comparing origins cannot distinguish "our card iframe" from "some other
 * opaque-origin frame on the page." `event.source` identity (the frame's `Window`
 * object reference) is the only reliable way to confirm a message actually came from
 * the iframe we rendered.
 */
export function isFromExpectedSource(event: MessageEvent, expectedSource: Window | null): boolean {
  return expectedSource !== null && event.source === expectedSource
}
