# TODO

## 0. Embedded CSS framework + fonts

Add a professional CSS framework and a few more font options. Everything must
be vendored locally — no CDN references. Candidates worth evaluating: a
utility framework (Tailwind via the Vite plugin so unused classes are purged)
or a tokens-only sheet (Open Props) layered on top of the existing CSS
variables. For fonts, vendor 1–2 sans options (Inter, IBM Plex Sans) and at
least one mono (JetBrains Mono, Fira Code) under `frontend/public/fonts/` and
register them via `@font-face` in `style.css`. Bundle impact: budget ~200KB
per font family across weights.

## 1. Streaming markdown flicker

Each streamed chunk re-parses the entire message. Fine in practice for short
responses, but for long responses with multiple code blocks it can cause
visible jank. Fix: debounce `renderMarkdown` for in-flight assistant messages
so the parse runs at most every ~50ms (or once on `done`). Track the
in-flight message by id and apply a cached/debounced render until streaming
completes, then a final clean render.

## 2. Link target — open in system browser

Markdown links currently open inside the Wails webview, replacing the app
content (wrong). Fix:

- In `markdown.ts`, post-process anchor tags to add `target="_blank"` and
  `rel="noopener noreferrer"`.
- On the Go side, register an `OnDomReady` handler in `main.go` (or a small
  bootstrap script in `index.html`) that intercepts navigation events for
  http(s) URLs and calls `runtime.BrowserOpenURL(ctx, url)` instead of
  letting the webview navigate.

## 3. DOMPurify v3 / SSR

Not actionable today — flagged for awareness. DOMPurify v3 works directly in
the Wails webview because there is a real `window`/`document`. If the
deployment model ever changes (e.g. server-side rendering, Node-side
pre-render of conversations, exporting transcripts to static HTML from a
build script), DOMPurify will need a `jsdom` shim:

```ts
import { JSDOM } from 'jsdom';
import createDOMPurify from 'dompurify';
const window = new JSDOM('').window;
const DOMPurify = createDOMPurify(window as unknown as Window);
```

No change required while the only consumer is the Wails webview.
