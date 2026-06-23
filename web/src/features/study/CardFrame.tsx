import { useEffect, useMemo, useRef } from 'react'

// Inline-bundled KaTeX assets so the sandboxed card iframe never makes an external
// network request (the iframe has an opaque origin and no `allow-same-origin`, so it
// has no meaningful origin to fetch "from" anyway — bundling keeps it fully
// self-contained and avoids a CSP headache later). The `?raw` suffix is a Vite
// feature: it imports the file's contents as a plain string instead of letting Vite
// process it as CSS/JS.
import katexCss from 'katex/dist/katex.min.css?raw'
import katexJs from 'katex/dist/katex.min.js?raw'
import autoRenderJs from 'katex/dist/contrib/auto-render.min.js?raw'

import { isFromExpectedSource, parseCardMessage, type CardMessage } from './cardBridge.ts'

// PRODUCTION HARDENING (deferred deploy follow-up, not built here): for MVP, card
// content is loaded via `srcdoc` on an `<iframe sandbox="allow-scripts">`, which
// already gives the frame a forced opaque origin (no `allow-same-origin`) and
// isolates it from the parent's DOM/cookies/localStorage. The hardened production
// version additionally serves card content from a genuinely separate origin/subdomain
// (e.g. `cards.usercontent.mindmesh.app`) with its own restrictive CSP, so that even a
// browser-engine bug in the sandbox attribute's enforcement has a second isolation
// layer to fall back on. That migration is out of scope for this MVP component.

// KaTeX's bundled CSS references its web font files via relative `url(fonts/...)`
// paths. Inside a `srcdoc` document those requests have no base URL to resolve
// against and will simply fail to load — math still renders (KaTeX falls back to
// default glyphs), just without the custom math fonts. Acceptable for MVP; revisit
// if font fidelity becomes a product requirement.

function buildSrcDoc(html: string): string {
  return `<!doctype html>
<html>
  <head>
    <meta charset="utf-8" />
    <style>${katexCss}</style>
    <script>${katexJs}</script>
    <script>${autoRenderJs}</script>
  </head>
  <body>
    ${html}
    <script>
      // Minimal dispatcher card content can call to talk to the host. Internally this
      // is the ONLY postMessage the frame ever sends, and it always targets '*':
      // the frame's origin is opaque (no allow-same-origin), so it has no specific
      // origin to address the parent by, and the message itself carries no secret
      // (card UI events only — never app/auth state, which this frame never has
      // access to in the first place). This is intentional, not an oversight.
      window.__mindmesh = {
        reveal: function () { window.parent.postMessage({ type: 'reveal' }, '*') },
        grade: function (payload) {
          window.parent.postMessage(Object.assign({ type: 'grade' }, payload), '*')
        },
        playAudio: function (payload) {
          window.parent.postMessage(Object.assign({ type: 'playAudio' }, payload), '*')
        },
        hint: function (payload) {
          window.parent.postMessage(Object.assign({ type: 'hint' }, payload), '*')
        },
      }
      renderMathInElement(document.body, {
        delimiters: [
          { left: '$', right: '$', display: false },
          { left: '\\\\[', right: '\\\\]', display: true },
        ],
      })
    </script>
  </body>
</html>`
}

export type CardFrameProps = {
  html: string
  onReveal?: () => void
  onGrade?: (message: CardMessage) => void
  onPlayAudio?: (message: CardMessage) => void
  onHint?: (message: CardMessage) => void
}

/**
 * Renders untrusted, author-authored card HTML/CSS/JS inside a sandboxed iframe and
 * relays a small set of card-initiated events (reveal/grade/playAudio/hint) back to
 * the host via postMessage. This component is the isolation boundary: it never
 * exposes app/auth state to the frame, never posts a message INTO the frame (it is
 * receive-only — host to frame communication is out of scope; only the `html` prop
 * crosses the boundary, and only outward-bound), and never evaluates card content
 * itself (the sandboxed iframe's own script execution carries that risk, contained by
 * `sandbox="allow-scripts"` with no `allow-same-origin`).
 */
export function CardFrame({ html, onReveal, onGrade, onPlayAudio, onHint }: CardFrameProps) {
  const iframeRef = useRef<HTMLIFrameElement>(null)
  const srcDoc = useMemo(() => buildSrcDoc(html), [html])

  useEffect(() => {
    function handleMessage(event: MessageEvent) {
      const expectedSource = iframeRef.current?.contentWindow ?? null
      if (!isFromExpectedSource(event, expectedSource)) {
        return
      }
      const message = parseCardMessage(event.data)
      if (message === null) {
        return
      }
      switch (message.type) {
        case 'reveal':
          onReveal?.()
          break
        case 'grade':
          onGrade?.(message)
          break
        case 'playAudio':
          onPlayAudio?.(message)
          break
        case 'hint':
          onHint?.(message)
          break
      }
    }

    window.addEventListener('message', handleMessage)
    return () => window.removeEventListener('message', handleMessage)
  }, [onReveal, onGrade, onPlayAudio, onHint])

  // NOTE for reviewers: this component must never call `iframeRef.current
  // ?.contentWindow?.postMessage(...)` anywhere — the bridge is receive-only
  // (frame -> host). Host -> frame messaging is intentionally out of scope.
  return <iframe ref={iframeRef} sandbox="allow-scripts" srcDoc={srcDoc} />
}
