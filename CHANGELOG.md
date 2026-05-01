# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Project scaffolding restructured into Go best-practice layout: `cmd/margo-cli/`,
  `pkg/margo/` (importable framework), `pkg/margo/providers/{anthropic,openai}/`,
  `internal/config/` (godotenv loader).
- `pkg/margo` provider-agnostic `Client` interface with `Complete` and `Stream`
  methods, multi-turn `Request{System, Messages, MaxTokens}`, and `Chunk{Text, Err}`
  for streaming.
- Anthropic provider (`pkg/margo/providers/anthropic`) with multi-turn and SSE streaming.
- OpenAI provider (`pkg/margo/providers/openai`) using Chat Completions API
  (switched from Responses API for cleaner multi-turn semantics) with streaming.
- Headless CLI (`cmd/margo-cli`) with `-provider`, `-prompt`, `-system`, `-stream` flags.
- Wails v2 desktop app integration (svelte-ts template):
  - `app.go` exposes `Providers()`, `Chat()`, `StreamChat(id, ...)`, `CancelStream(id)`
    to the frontend; streaming uses Wails events
    (`margo:stream:<id>:{chunk,error,done}`).
  - Caller-provided stream ids eliminate the subscribe-after-emit race.
  - Per-stream cancellation via `context.WithCancel` tracked in a mutex-guarded map.
- Three-pane LM Studio-inspired UI: collapsible left chat-history sidebar,
  centre conversation area, collapsible right model-parameters sidebar.
- Multiple persisted conversations via Svelte stores backed by `localStorage`
  (`margo:chats:v1`, `margo:settings:v1`); auto-titling from first user message;
  rename and two-click confirm-delete in the chat list.
- Light theme (default) + dark theme toggle, persisted; CSS variables drive both.
- Markdown rendering via `marked` + `marked-highlight` + `highlight.js` (common
  language subset, ~30 langs) with `dompurify` sanitization.
- Math rendering via vendored MathJax 3 (`tex-svg-full.js`, ~2.2MB, SVG output,
  no external font loads) under `frontend/public/mathjax/`.
- Math/markdown interop: pre-extraction of `$...$`, `$$...$$`, `\(...\)`,
  `\[...\]` (and code blocks) before `marked` runs, with restoration into the
  rendered HTML — preserves LaTeX backslashes that `marked` would otherwise
  consume as escape sequences.
- Streaming UI: token-by-token assistant rendering with blinking cursor,
  cancel button, auto-scroll to bottom, debounced (250ms) MathJax typeset
  after stream pauses.
- Auto-create chat on first message — no need to manually create an empty
  chat before typing.
- Light/dark `highlight.js` themes (`github`, `github-dark`) swapped at
  runtime via a single injected `<style>` element.
- `Makefile` with targets for Wails (`dev`, `build`, `build-debug`,
  `build-universal`, `package`, `run`, `bindings`), CLI (`cli`, `cli-run`),
  Go (`tidy`, `fmt`, `vet`, `test`, `cover`, `lint`), frontend
  (`frontend-install`, `frontend-dev`, `frontend-build`, `vendor-mathjax`),
  cleanup (`clean`, `clean-frontend`, `clean-all`), and `doctor` for
  toolchain diagnostics.
- `.env.example` with `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` placeholders.
- `TODO.md` capturing follow-ups (CSS framework + fonts, streaming markdown
  debounce, system-browser link handling, DOMPurify SSR notes).

### Changed

- Wails app background colour from dark blue to white to match the light
  default theme during initial paint.

### Fixed

- Math rendering: backslash-eating bug where `marked` consumed `\[`, `\\`,
  `\times`, `\neq` etc. before MathJax could process them — now math is
  pre-extracted and reinjected post-parse.
- Chat deletion: `window.confirm` is suppressed in the Wails webview, so
  deletes silently failed. Replaced with an inline two-click confirm pattern
  ("×" → "sure?" with 3-second arming window).
- `deleteChat` simplified — removed nested `chats.update` inside
  `activeChatId.update`; single pass computes the next-active id while
  filtering.

[Unreleased]: https://github.com/shakfu/margo
