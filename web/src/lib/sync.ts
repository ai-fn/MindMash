// Lightweight SSE-over-fetch subscription to GET /v1/sync/stream (Task 8, see
// task-8-brief.md). Native `EventSource` cannot set custom headers, and this endpoint
// requires `Authorization: Bearer <token>` like every other call — so this hand-rolls
// stream parsing over `fetch()` instead of using `EventSource`. No SSE client library:
// the wire format here is trivial (two event names, no `id:`/reconnection semantics).
//
// Mirrors the seam-not-store-coupling convention from `lib/api/mutator.ts`: this module
// reads the access token via `authToken.getAccessToken()` and never imports the Zustand
// auth store.
import { getAccessToken } from './api/authToken'

const STREAM_PATH = '/v1/sync/stream'
const RECONNECT_DELAY_MS = 3000

/**
 * Subscribes to the server's sync-changed notifications. Calls `onChanged()` once per
 * `event: changed` SSE event received. `event: ready` (sent immediately on connect) is
 * ignored — the initial app-start data fetch already happens independently of this
 * stream, so there's nothing additional to do on `ready`.
 *
 * Returns an unsubscribe function that aborts the in-flight fetch and cancels any
 * pending reconnect timer. Safe to call multiple times.
 */
export function subscribeSyncChanges(onChanged: () => void): () => void {
  let aborted = false
  let abortController: AbortController | null = null
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null

  function scheduleReconnect() {
    if (aborted) {
      return
    }
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null
      connect()
    }, RECONNECT_DELAY_MS)
  }

  async function connect() {
    if (aborted) {
      return
    }
    const accessToken = getAccessToken()
    if (!accessToken) {
      // No point opening the stream before login; the effect that owns this
      // subscription's lifetime is gated on auth status, but guard here too in case
      // the token clears between schedule and fire.
      scheduleReconnect()
      return
    }

    abortController = new AbortController()
    let response: Response
    try {
      response = await fetch(STREAM_PATH, {
        headers: { Authorization: `Bearer ${accessToken}` },
        signal: abortController.signal,
      })
    } catch {
      if (!aborted) {
        scheduleReconnect()
      }
      return
    }

    if (!response.ok || !response.body) {
      if (!aborted) {
        scheduleReconnect()
      }
      return
    }

    const reader = response.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''

    try {
      while (true) {
        const { done, value } = await reader.read()
        if (done) {
          break
        }
        buffer += decoder.decode(value, { stream: true })

        let boundary = buffer.indexOf('\n\n')
        while (boundary !== -1) {
          const block = buffer.slice(0, boundary)
          buffer = buffer.slice(boundary + 2)
          handleEventBlock(block)
          boundary = buffer.indexOf('\n\n')
        }
      }
    } catch {
      // Aborted or network error mid-stream — fall through to reconnect below.
    }

    if (!aborted) {
      scheduleReconnect()
    }
  }

  function handleEventBlock(block: string) {
    if (aborted) {
      return
    }
    const lines = block.split('\n')
    const isChanged = lines.some((line) => line.trim() === 'event: changed')
    if (isChanged) {
      onChanged()
    }
  }

  connect()

  return function unsubscribe() {
    aborted = true
    if (reconnectTimer !== null) {
      clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
    abortController?.abort()
  }
}
