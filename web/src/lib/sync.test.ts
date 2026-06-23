import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { getAccessToken } from './api/authToken'
import { subscribeSyncChanges } from './sync'

vi.mock('./api/authToken', () => ({
  getAccessToken: vi.fn(),
}))

// Builds a fetch Response whose body is a controllable ReadableStream<Uint8Array>, so
// tests can push SSE chunks at will via `push` and end the stream via `close`.
function controllableStreamResponse(): {
  response: Response
  push: (chunk: string) => void
  close: () => void
} {
  let controller: ReadableStreamDefaultController<Uint8Array>
  const stream = new ReadableStream<Uint8Array>({
    start(c) {
      controller = c
    },
  })
  const encoder = new TextEncoder()
  const response = new Response(stream, { status: 200 })
  return {
    response,
    push: (chunk: string) => controller.enqueue(encoder.encode(chunk)),
    close: () => controller.close(),
  }
}

describe('subscribeSyncChanges', () => {
  beforeEach(() => {
    vi.mocked(getAccessToken).mockReturnValue('access-123')
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it('fires onChanged when a "changed" SSE event arrives', async () => {
    const { response, push } = controllableStreamResponse()
    const fetchMock = vi.fn().mockResolvedValue(response)
    vi.stubGlobal('fetch', fetchMock)

    const onChanged = vi.fn()
    const unsubscribe = subscribeSyncChanges(onChanged)

    push('event: ready\ndata: {}\n\n')
    push('event: changed\ndata: {}\n\n')
    // Let the reader's microtask queue drain so the pushed chunks are processed.
    await vi.waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1))

    unsubscribe()
  })

  it('does not fire onChanged for a "ready" event', async () => {
    const { response, push } = controllableStreamResponse()
    const fetchMock = vi.fn().mockResolvedValue(response)
    vi.stubGlobal('fetch', fetchMock)

    const onChanged = vi.fn()
    const unsubscribe = subscribeSyncChanges(onChanged)

    push('event: ready\ndata: {}\n\n')
    // Give the stream a chance to be read; there's no "changed" event to wait for, so
    // flush a microtask tick instead.
    await new Promise((resolve) => setTimeout(resolve, 0))

    expect(onChanged).not.toHaveBeenCalled()
    unsubscribe()
  })

  it('stops calling onChanged after unsubscribe even if more chunks arrive', async () => {
    const { response, push } = controllableStreamResponse()
    const fetchMock = vi.fn().mockResolvedValue(response)
    vi.stubGlobal('fetch', fetchMock)

    const onChanged = vi.fn()
    const unsubscribe = subscribeSyncChanges(onChanged)

    push('event: changed\ndata: {}\n\n')
    await vi.waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1))

    unsubscribe()

    push('event: changed\ndata: {}\n\n')
    await new Promise((resolve) => setTimeout(resolve, 0))

    expect(onChanged).toHaveBeenCalledTimes(1)
  })

  it('never calls fetch when there is no access token', async () => {
    vi.mocked(getAccessToken).mockReturnValue(null)
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)

    const unsubscribe = subscribeSyncChanges(vi.fn())
    await new Promise((resolve) => setTimeout(resolve, 0))

    expect(fetchMock).not.toHaveBeenCalled()
    unsubscribe()
  })
})
