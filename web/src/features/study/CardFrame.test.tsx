import { render, cleanup } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { CardFrame } from './CardFrame.tsx'

afterEach(() => {
  cleanup()
})

function getIframe(container: HTMLElement): HTMLIFrameElement {
  const iframe = container.querySelector('iframe')
  if (iframe === null) {
    throw new Error('expected an iframe to be rendered')
  }
  return iframe
}

describe('CardFrame', () => {
  it('renders an iframe with sandbox="allow-scripts" (exact value) and the given html in srcdoc', () => {
    const { container } = render(<CardFrame html="<p>front</p>" />)
    const iframe = getIframe(container)

    expect(iframe.getAttribute('sandbox')).toBe('allow-scripts')
    expect(iframe.srcdoc).not.toBe('')
    expect(iframe.srcdoc).toContain('<p>front</p>')
  })

  it('never sets allow-same-origin under any circumstance', () => {
    const { container } = render(<CardFrame html="<p>front</p>" />)
    const iframe = getIframe(container)

    expect(iframe.getAttribute('sandbox')).not.toContain('allow-same-origin')
  })

  it('fires onReveal when a valid reveal message arrives from the iframe contentWindow', () => {
    const onReveal = vi.fn()
    const { container } = render(<CardFrame html="<p>front</p>" onReveal={onReveal} />)
    const iframe = getIframe(container)

    window.dispatchEvent(
      new MessageEvent('message', { data: { type: 'reveal' }, source: iframe.contentWindow }),
    )

    expect(onReveal).toHaveBeenCalledTimes(1)
  })

  it('fires onGrade with the full message payload when a valid grade message arrives', () => {
    const onGrade = vi.fn()
    const { container } = render(<CardFrame html="<p>front</p>" onGrade={onGrade} />)
    const iframe = getIframe(container)

    window.dispatchEvent(
      new MessageEvent('message', {
        data: { type: 'grade', score: 3 },
        source: iframe.contentWindow,
      }),
    )

    expect(onGrade).toHaveBeenCalledTimes(1)
    expect(onGrade).toHaveBeenCalledWith({ type: 'grade', score: 3 })
  })

  it('ignores a malformed-shape message even from the correct source (string data)', () => {
    const onReveal = vi.fn()
    const { container } = render(<CardFrame html="<p>front</p>" onReveal={onReveal} />)
    const iframe = getIframe(container)

    window.dispatchEvent(
      new MessageEvent('message', { data: 'not-an-object', source: iframe.contentWindow }),
    )

    expect(onReveal).not.toHaveBeenCalled()
  })

  it('ignores a message with an unknown type even from the correct source', () => {
    const onReveal = vi.fn()
    const onGrade = vi.fn()
    const onPlayAudio = vi.fn()
    const onHint = vi.fn()
    const { container } = render(
      <CardFrame
        html="<p>front</p>"
        onReveal={onReveal}
        onGrade={onGrade}
        onPlayAudio={onPlayAudio}
        onHint={onHint}
      />,
    )
    const iframe = getIframe(container)

    window.dispatchEvent(
      new MessageEvent('message', {
        data: { type: 'somethingElse' },
        source: iframe.contentWindow,
      }),
    )

    expect(onReveal).not.toHaveBeenCalled()
    expect(onGrade).not.toHaveBeenCalled()
    expect(onPlayAudio).not.toHaveBeenCalled()
    expect(onHint).not.toHaveBeenCalled()
  })

  it('ignores a validly-shaped message from a foreign source (different window)', () => {
    const onReveal = vi.fn()
    render(<CardFrame html="<p>front</p>" onReveal={onReveal} />)
    const foreignSource = {} as Window

    window.dispatchEvent(
      new MessageEvent('message', { data: { type: 'reveal' }, source: foreignSource }),
    )

    expect(onReveal).not.toHaveBeenCalled()
  })

  it('ignores a validly-shaped message with a null source', () => {
    const onReveal = vi.fn()
    render(<CardFrame html="<p>front</p>" onReveal={onReveal} />)

    window.dispatchEvent(new MessageEvent('message', { data: { type: 'reveal' }, source: null }))

    expect(onReveal).not.toHaveBeenCalled()
  })

  it('never calls postMessage on the iframe contentWindow (receive-only bridge)', () => {
    const { container } = render(<CardFrame html="<p>front</p>" onReveal={vi.fn()} />)
    const iframe = getIframe(container)
    const contentWindow = iframe.contentWindow
    if (contentWindow !== null) {
      const postMessageSpy = vi.spyOn(contentWindow, 'postMessage')

      window.dispatchEvent(
        new MessageEvent('message', { data: { type: 'reveal' }, source: contentWindow }),
      )

      expect(postMessageSpy).not.toHaveBeenCalled()
    }
  })

  it('removes the window message listener on unmount', () => {
    const onReveal = vi.fn()
    const { container, unmount } = render(<CardFrame html="<p>front</p>" onReveal={onReveal} />)
    const iframe = getIframe(container)
    const contentWindow = iframe.contentWindow

    unmount()

    window.dispatchEvent(
      new MessageEvent('message', { data: { type: 'reveal' }, source: contentWindow }),
    )

    expect(onReveal).not.toHaveBeenCalled()
  })
})
