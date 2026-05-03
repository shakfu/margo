# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Eino orchestration layer (`pkg/margo/agent`) bridging margo's chat
  clients to the CloudWeGo Eino framework. `Adapter` exposes any
  `margo.Client` as `model.ToolCallingChatModel` (Generate + Stream +
  immutable `WithTools`); `*schema.ToolInfo` parameters are converted
  to `margo.ToolDef.Parameters` via `ParamsOneOf.ToJSONSchema()` +
  JSON round-trip. `Chat` / `ChatStream` entry points wrap simple
  flows; `React` and `StreamReact` run a `react.NewAgent` loop with
  tool callbacks emitting `StepEvent{Kind: text|tool_call|
  tool_result|error|done}`. Built-in `current_time` tool included as
  proof-of-life.
- Tool calling end-to-end across all three first-party providers.
  `margo.Request` gains `Tools []ToolDef` and `ToolChoice string`;
  `margo.Message` gains `ToolCalls []ToolCall` and `ToolCallID`;
  `margo.Response` gains `ToolCalls`; new `RoleSystem` / `RoleTool`
  roles and `ChunkToolCall` chunk kind. OpenAI and OpenRouter wire
  tools via `ChatCompletionFunctionTool` and accumulate per-index
  tool-call deltas in streaming. Anthropic wires tools via
  `ToolUnionParam{OfTool: ...}`, batches consecutive `RoleTool`
  messages into a single user message with multiple `tool_result`
  blocks (required by the Claude API), and accumulates `tool_use`
  deltas across `content_block_start` / `input_json_delta` /
  `content_block_stop` events.
- Agent mode in the desktop UI. `App.StreamAgent(id, provider, system,
  messages, opts, toolNames)` runs the ReAct loop and emits step
  events on the existing `margo:stream:<id>:chunk` channel; `App.Tools()`
  returns the registry of available tool names. `Message.steps?:
  AgentStep[]` extends the persisted chat schema; `appendStepToLast` /
  `updateLastStepResult` pair tool_call/tool_result events into a
  single step entry. Composer gains an "agent mode" checkbox (visible
  only when tools are configured) that routes `send` through
  `StreamAgent` instead of `StreamChat`. Tool calls render as
  monospace cards above the assistant content showing `→ name(args)`
  and `← result` (or "running…" while in flight).
- OpenRouter provider (`pkg/margo/providers/openrouter`) using the
  existing `openai-go/v3` SDK pointed at
  `https://openrouter.ai/api/v1` with `HTTP-Referer` / `X-Title`
  headers. Reads `OPENROUTER_API_KEY` from env / `.env` via
  `internal/config`. Default model `deepseek/deepseek-v3.2`; 17-model
  allowlist exposed via `App.Models("openrouter")` (DeepSeek, Gemini,
  Gemma, Kimi, Nemotron, OWL, Qwen, Grok families).
- Vendored fonts under `frontend/public/fonts/` (no CDN). Merriweather
  (serif, 3-axis variable: opsz/wdth/wght 300–900) for markdown body
  text via `--font-serif`; Merriweather Sans (variable wght 300–800)
  for UI chrome via `--font-sans`; JetBrains Mono (variable wght
  100–800) for code via `--font-mono`. All include true italic
  variants. Source TTFs subsetted to Latin coverage via `pyftsubset`
  (Merriweather 4.4MB → 760KB upright, 756KB italic). OFL licenses
  bundled alongside the woff2s.
- Melt UI confirm dialog for chat deletion. The previous inline
  "sure?" two-click pattern is replaced by a centered alertdialog
  with proper focus trap, Escape, and click-outside handling. The
  `×` button is now an inline 12×12 outline trash-can SVG.
- Tailwind CSS v4 + Melt UI for the desktop frontend. CSS variables
  registered as `@theme inline` tokens (`bg-bg`, `text-fg-muted`,
  `border-border`, etc.) so existing `:root.dark` theming still drives
  every utility. Three Melt UI primitives in use: two `createSelect`
  builders (Provider, Model) and six `createCollapsible` instances for
  the right-pane accordion sections. `@melt-ui/pp` chained after
  `svelte-preprocess` via `svelte-sequential-preprocessor`.
- Sampling parameters end-to-end. `margo.Request` gains
  `Temperature *float64`, `TopP *float64`, `StopSequences []string`,
  and `Thinking *Thinking{Enabled, BudgetTokens}`. Both providers wire
  these through their respective SDKs. `app.go` exposes a new
  `ChatOptions` struct on `Chat()` / `StreamChat()`. Right-pane
  Sampling accordion: temperature slider (0..2), top-p slider (0..1),
  max tokens, stop sequences (comma-separated). Reset buttons revert a
  slider to provider default (`null` -> omitted from request).
- Extended thinking (Anthropic Claude 3.7+ / 4.x). New Thinking
  accordion with enable toggle (off by default) and budget input
  (min 1024). When enabled, `ThinkingConfigParamOfEnabled(budget)` is
  passed to the Messages API and `thinking_delta` events stream as a
  separate `Chunk{Kind: thinking}` channel. Frontend renders thinking
  in a `<details>` block above each assistant message; collapses
  automatically once streaming completes. Topbar shows a `thinking`
  badge while enabled.
- Token accounting + per-message stats. `margo.Usage{InputTokens,
  OutputTokens, FirstTokenMs, TotalMs}` is populated by both providers
  (Anthropic from `message_start`/`message_delta`; OpenAI from
  `stream_options.include_usage=true`). The streaming protocol's
  `:done` event payload now carries `{usage}`, and the per-chunk
  payload becomes `{kind, text}`. Each assistant bubble shows a
  footer with `tok/s`, total tokens, time-to-first-token, and total
  latency. `Chat.tokensIn` / `Chat.tokensOut` totals persist in
  `localStorage`.
- Context-usage ring next to the composer. Conic-gradient over the
  background colour, fed by `(tokensIn + tokensOut) /
  contextWindowFor(model)`. Per-model context windows hard-coded in
  `frontend/src/lib/store.ts`.
- Model picker per provider. New `App.Models(provider)` Wails binding
  returns the allowlist; the Settings panel populates a Melt UI
  Select from it. Stale persisted models (not in the current
  allowlist) auto-reset to the provider's default on next load.
  - Anthropic: `claude-haiku-4-5` (default), `claude-sonnet-4-6`,
    `claude-opus-4-7`.
  - OpenAI: `gpt-5.4-nano` (default), `gpt-5.4-mini`, `gpt-5.4`,
    `gpt-5.4-pro`, `gpt-5.5`, `gpt-5.5-pro`.
- Topbar badges for the currently-selected provider, model, and
  thinking state.
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

- macOS bundle and window title display as `Margo` (capitalized).
  Added `info.productName: "Margo"` to `wails.json` (drives
  `CFBundleName` for both production and dev plists) and updated
  `options.App.Title` in `main.go`. Module name, binary name, and
  config keys remain lowercase.
- Default markdown body font is now Merriweather (serif) — chat
  responses use `var(--font-serif)` so prose reads in a serif while
  UI chrome (sidebar, composer, settings) stays in Merriweather Sans
  via `var(--font-sans)`. Code blocks pick up `var(--font-mono)`
  (JetBrains Mono) automatically.
- `StreamChat`'s chunk payload carries a third event family beyond
  `text` / `thinking`: agent runs additionally emit
  `tool_call` / `tool_result` chunks. The frontend handler now
  dispatches by `payload.kind`; consumers of the bus that ignored
  unknown kinds continue to work unchanged.
- Frontend toolchain upgraded: Svelte 3.49 -> 4.2, Vite 3 -> 5,
  TypeScript 4.6 -> 5.5, `@sveltejs/vite-plugin-svelte` 1 -> 3,
  `svelte-preprocess` 4 -> 6, `svelte-check` 2 -> 3.
- Streaming protocol breaking change. Previously `margo:stream:<id>:chunk`
  payload was a raw string and `:done` payload was nil. Now `:chunk`
  carries `{kind: "text" | "thinking", text: string}` and `:done`
  carries `{usage: StreamUsage | null}`. CLI consumer (`cmd/margo-cli`)
  is unaffected because it consumes the in-process `Chunk` channel,
  not the Wails event bus.
- All component-level hand-rolled CSS in `App.svelte`, `ChatList.svelte`,
  and `SettingsPanel.svelte` replaced with Tailwind utilities. Markdown
  `:global(...)` styles preserved in `App.svelte` since they target
  innerHTML produced by `marked`.
- Right pane gains four new accordion sections (Model, Sampling,
  Thinking, plus the existing Provider, System Prompt, Appearance);
  Sampling and Thinking default to collapsed.
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
- Melt UI integration required `@melt-ui/pp` preprocessor; without it
  every `use:melt={$store}` action threw at component mount and broke
  Svelte's app-wide event delegation (textarea typing worked, every
  click handler was dead).
- Melt UI Select dropdown rendered twice when `forceVisible: true` was
  combined with `{#if $open}` — Melt portalled one copy and the
  conditional rendered another. Removed `forceVisible` since the menu
  is already conditionally rendered.
- Svelte 4 type tightening: `new App({target: document.getElementById('app')})`
  required a `!` non-null assertion since `getElementById` returns
  `HTMLElement | null` and Svelte 4's `target` no longer accepts that.

[Unreleased]: https://github.com/shakfu/margo
