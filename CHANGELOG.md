# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Multimodal-model gate (TODO §7.3). New `MULTIMODAL_MODELS` set +
  `isMultimodal(model)` helper in `store.ts` (alongside
  `CONTEXT_WINDOWS`) seeded with Anthropic Claude 4.x and OpenAI
  GPT-5.x families. Composer disables the send button and surfaces
  an inline error banner when attachments are pending against a
  text-only model; the `send()` guard short-circuits with a matching
  error to cover the keyboard-shortcut path. Avoids the previous
  failure mode of silently shipping an image to a model that drops
  it (or errors at the provider with a less-clear message).
  Allowlist maintained alongside the model menus in `app.go`.
- Image attachments end-to-end (TODO §7.1 + §7.2). The composer
  gains a paperclip button and drag-drop zone for attaching images
  (PNG, JPEG, WebP, GIF; 10 MB per file). Frontend reads via the
  browser File API + FileReader, base64-encodes, and forwards a new
  `AttachmentInput[]` arg to `Chat` / `StreamChat` / `StreamAgent`.
  `pkg/margo` gains a `Part` type
  (`Kind: text|image|document`, `Text`, `MimeType`, `Data []byte`)
  and `Message.Parts []Part`; providers prefer `Parts` when
  non-empty, falling back to the legacy `Content string`. Anthropic
  emits `NewImageBlockBase64`; OpenAI / OpenRouter use
  `data:<mime>;base64,...` `image_url` parts. The agent path threads
  attachments through a new `Adapter.WithFinalUserAttachments` that
  stamps them onto the last user turn at request time (necessary
  because eino's `schema.Message` doesn't carry our Parts shape).
  Attachments are not persisted in chat history — they're sent once
  and cleared (`Message.attachmentCount` records the count for the
  user-bubble badge). Documents (`PartDocument`) are reserved but
  not yet routed; deferred to §7.5. Cross-provider parity is
  shipped at the wire layer; see the §7.3 entry above for the
  multimodal-model gate that warns and disables send when the
  active model is text-only. Coverage:
  `adapter_test.go::TestAdapterFinalUserAttachments` plus
  per-provider conversion paths. Design lives in `TODO.md` §7
  (no separate design doc).
- Agents (TODO §8.2). Personas with a tool allowlist; route through
  `StreamAgent` (the ReAct loop) with the allowlist substituted for
  "all available tools". New `Agent` interface +
  `Settings.agents: Agent[]` carry a `BUILTIN_AGENTS` catalog
  (Quarto Author → `quarto_render`; Time-aware assistant →
  `current_time`) merged on every load alongside any custom agents.
  `Chat.agentId?: string` carries the per-chat selection,
  mutually exclusive with `personaId` (set-helpers clear the
  opposite field). Role picker in the composer topbar grows an
  Agents optgroup; entries show `Name [N]` and grey out via
  `agentMissingTools` when their tool list isn't currently
  registered (e.g. quarto isn't installed) with an inline
  "needs X" hint. Settings → Agents grows an "Agents" section with
  Edit / Duplicate / Delete actions + a tool-allowlist checkbox
  group in the create/edit dialog. Validation: empty allowlist
  rejected at save. Active agent's tool list surfaces under the
  composer when an agent is picked. The legacy `agentMode`
  checkbox is removed from the composer footer — selecting an
  agent is the new way to enable tools. The persisted
  `Settings.agentMode` flag still drives the route for backwards
  compat on chats that had it set before this change. Design doc:
  `docs/dev/personas_and_agents.md`.
- Personas (TODO §8.1). Tool-less roles that swap the system prompt
  on a per-chat basis. New `Persona` interface +
  `Settings.personas: Persona[]` with a four-entry builtin catalog
  (Editor, Code Reviewer, Researcher, Concise) merged into the
  persisted list on every load — deleting a builtin by hand-editing
  storage doesn't stick. `Chat.personaId?: string` carries the
  per-chat selection (mutually exclusive with the future
  `agentId`). Role picker is a native `<select>` in the topbar
  showing **Default** plus a Personas optgroup; switching swaps the
  system prompt that goes to `StreamChat` / `StreamAgent` on the
  next request. Persona's `systemPrompt` fully *replaces*
  `Settings.system` rather than prepending — the deliberate design
  call documented in
  `docs/dev/personas_and_agents.md` § "System-prompt resolution".
  Settings → Agents grows a "Personas" section with Edit /
  Duplicate / Delete actions (builtins are duplicate-only) and a
  shared Melt UI dialog for create + edit. Builtin ids are stable
  (`builtin-editor`, etc.) so chat references survive ship-version
  updates. No Go-side changes — persona resolution is entirely
  frontend. Design doc:
  `docs/dev/personas_and_agents.md`.
- Right pane is now tabbed and titled "Settings" instead of "Model
  Parameters" — the panel had grown well beyond model knobs (output
  directory, trusted tools, reset). Three Melt UI tabs split the
  sections by what they affect: **Models** (Provider, Model,
  Sampling, Thinking — model selection + parameters), **Agents**
  (Trusted tools — agent / tool-related state) and **General**
  (System Prompt, Appearance, Output, Reset — everything else).
  Active-tab styling uses `data-state="active"` set by Melt's
  createTabs builder.
- Tool permission prompts. State-mutating agent tools (anything that
  isn't on the explicit `agent.ReadOnlyTools` allowlist) now require
  user approval before execution. New `agent.permissionMiddleware`
  registered alongside `abortOnCtxCancel` in the React loop's
  `ToolsConfig.ToolCallMiddlewares`. When the gate fires, the Go
  side emits a `permission` step event carrying a unique id, blocks
  on a channel registered in `App.permissions`, and honours ctx
  cancellation. The frontend renders the prompt as a step card with
  Approve / Always / Deny buttons; clicking calls a new
  `App.RespondPermission(id, approved, always)` Wails method to
  deliver the decision. The "Always" choice persists in
  `Settings.autoApproveTools` (localStorage) and is forwarded to
  `App.StreamAgent` on subsequent runs so the prompt doesn't
  reappear. `current_time` is on the read-only allowlist;
  `quarto_render` requires approval. `App.StreamAgent` signature
  gained an `autoApprove []string` parameter. Coverage:
  `permission_test.go::TestPermissionGate*` (approve/deny paths,
  read-only bypass, cancellation while pending). Settings panel
  gains a collapsible "Trusted tools" section listing the persisted
  `autoApproveTools` entries with per-entry Revoke buttons and a
  Revoke-all shortcut, so users can manage approvals without
  resetting the app or hand-editing localStorage. Docs:
  `docs/dev/agents_and_tools.md` § "Tool permission prompts".
- Context-window management. Long conversations no longer overflow
  silently — `pkg/margo/agent/budget.go` ships a budget-aware message
  rewriter wired into both the agent path
  (`react.AgentConfig.MessageRewriter`, runs between ReAct iterations
  so tool results accumulating mid-loop also get trimmed) and the
  plain-chat path (`toMargoRequest` in `app.go`). Trims oldest turns
  first under a 25% output reserve, preserves the system prompt and
  the final user turn, and groups Tool messages with their owning
  Assistant turn so tool_results are never orphaned from their
  tool_call. Token estimation is chars/4 (coarse but no tokenizer
  dep). Per-model context budgets (`BudgetForModel`) mirror the
  frontend's `CONTEXT_WINDOWS` table by hand; unknown models fall
  back to 128k. Coverage: `TestRewriteForBudget*`,
  `TestRewriteMargoForBudget`. Docs:
  `docs/dev/agents_and_tools.md` § "Context-window management".
- `quarto_render` agent tool. Wraps the local `quarto` CLI to convert
  `.qmd` / `.md` / `.ipynb` documents (or quarto project directories) to
  html, pdf, docx, pptx, revealjs, beamer, latex, typst, and other
  pandoc targets. Two modes: **render existing** (pass `input`) or
  **create-and-render** (pass `content` — full Quarto source including
  YAML frontmatter — which the tool writes to disk before invoking
  quarto, since margo has no separate file-write tool). Create-and-
  render outputs land in `~/Documents/Margo/outputs/` (margo's stable
  per-user output directory, created on first use), with the filename
  derived from the YAML `title:` (e.g. "How to Boil an Egg" →
  `how-to-boil-an-egg.qmd` → `how-to-boil-an-egg.pptx`). Subsequent
  renders of the same title get a `-2` / `-3` / … suffix instead of
  silently overwriting the previous artifact. Format strings are
  checked against an allowlist before reaching pandoc; the render runs
  with a 10-minute deadline and honors ctx cancellation. The tool
  result parses quarto's `Output created:` line, resolves it to an
  absolute path, and appends a ready-to-paste markdown link (`[<basename>](file://<abs-path>)`)
  with explicit instructions to the model that it must surface this
  verbatim — bolding or relative-path links don't render as clickable
  in the assistant bubble. The tool is registered conditionally —
  `app.go` calls `agent.QuartoAvailable()` at process start and only
  adds it to `builtinTools` when `quarto` is on `PATH`; margo does not
  bundle the binary. New `App.OutputDir()` Wails binding lets the
  frontend display the path and offers an **"Open in Finder"** action
  in the right pane's new "Output" section (uses `BrowserOpenURL`
  with a `file://` URL). Coverage: `TestQuartoRenderArgValidation`,
  `TestSlugFromContent`, `TestQuartoRenderHTML`,
  `TestQuartoRenderCreateAndRender`, `TestQuartoRenderTitleSlug` (now
  also asserts the configured-dir path and the `-2` collision suffix);
  live-render tests skip when quarto is unavailable.
- App-level reset. New "Reset" section in the right pane (Melt UI
  collapsible + alertdialog confirm) that cancels any in-flight
  stream via `CancelStream`, removes `margo:chats:v1` and
  `margo:settings:v1` from `localStorage`, and reloads the frontend.
  The Wails Go process keeps running across the reload, so the
  pre-reload cancel is load-bearing — without it the prior stream's
  events would land in a freshly-initialised UI.
- Dismiss button on the error banner. The banner now ships an inline
  `×` that clears `error` state, so a sticky error from a failed
  startup or stream no longer requires sending a fresh message to
  dismiss.
- `file://` URLs are now clickable in assistant markdown. DOMPurify's
  `ALLOWED_URI_REGEXP` is overridden to permit `file:` alongside
  `https?:` / `mailto:` (default sanitization stripped them).
  Wails v2's `BrowserOpenURL` rejects non-http(s)/mailto schemes
  outright ("Invalid URL scheme not allowed"), so a new Go-side
  `App.OpenPath(path)` Wails binding handles file paths by shelling
  out to the OS-native opener (`open` on macOS, `cmd /c start` on
  Windows, `xdg-open` elsewhere). The capture-phase document click
  handler in `App.svelte` strips the `file://` prefix, decodes the
  URI, and routes to `OpenPath`; `http(s):` / `mailto:` continue
  through `BrowserOpenURL` unchanged. The Settings panel's "Open in
  Finder" button (Output section) uses `OpenPath` directly. Enables
  agent tools that produce local artifacts (notably `quarto_render`)
  to surface clickable links that open in the user's default app
  (`.pptx` → PowerPoint, `.html` → default browser, dirs → Finder,
  etc.).
- Mid-loop text streaming for ReAct agent runs (TODO #6.1).
  `pkg/margo/agent/stream.go` registers a
  `utils/callbacks.ModelCallbackHandler.OnEndWithStreamOutput` that
  drains each intermediate model turn synchronously, accumulates
  `Message.Content`, and emits a single `StepText` event before the
  same turn's tool callbacks fire — previously the model's reasoning
  ("Let me check the time first.") was dropped because
  `react.Agent.Stream` only forwards the final turn. Synchronous drain
  guarantees text-then-tool ordering. Final-turn dedup is gated on
  "this turn produced tool calls"; the gate's load-bearing assumption
  is documented in `docs/dev/agents_and_tools.md` § "Mid-loop text
  streaming". Coverage: `TestStreamReactMidLoopTextOrdering`.
- Prompt cancellation for in-flight ReAct tool calls (TODO #6.2,
  supersedes the original TODO #4). New `abortOnCtxCancel`
  `compose.ToolMiddleware` on the ReAct ToolsNode races each invokable
  tool against `ctx.Done()` and returns `ctx.Err()` immediately on
  cancel, unblocking the React loop without waiting for misbehaving
  tools that ignore ctx. Investigated Eino's interrupt machinery
  (`compose.WithInterruptBeforeNodes`, `core.InterruptSignal`) and
  confirmed it is for human-in-the-loop checkpoint/resume, not
  cancellation — middleware was the correct path. UI: composer flips
  a `cancelling` flag on click, relabels the button "cancelling…" and
  disables it until the run unwinds via `:done`/`:error`. Coverage:
  `TestStreamReactCancelMidTool` (5-second sleep tool, asserts return
  within 2s of cancel). Docs:
  `docs/dev/agents_and_tools.md` § "Cancellation".
- Streaming markdown render throttle (TODO #1).
  `renderMarkdownStreaming(text, streaming)` in
  `frontend/src/lib/markdown.ts` caps the parse rate to once per 50ms
  while the in-flight assistant message is updating, returning a
  single-slot cached HTML for intra-throttle calls. When `streaming`
  flips to false the cache is cleared and a fresh clean parse runs on
  the trailing edge (driven by Svelte's reactive re-eval when `busy`
  changes). Eliminates visible jank on long responses with multiple
  code blocks. (MathJax is already debounced 250ms in
  `frontend/src/lib/mathjax.ts`, so it doesn't compound the cost.)
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

- Markdown links opened inside the Wails webview, replacing the app
  shell with the destination page (TODO #2). DOMPurify's
  `afterSanitizeAttributes` hook now injects `target="_blank"` and
  `rel="noopener noreferrer"` on anchors with `http(s):` / `mailto:`
  hrefs; a capture-phase document click handler in `App.svelte`
  intercepts those anchors, prevents default navigation, and routes
  the URL through Wails' `BrowserOpenURL` so it opens in the user's
  default system browser. Internal `#fragment` and relative links are
  left untouched.
- ReAct loop ended after the first model turn whenever the model
  emitted preamble text before its tool call (typical for Claude:
  "Let me check the time first." → tool_use). Eino's default
  `firstChunkStreamToolCallChecker` only inspects the first content
  chunk, so it classified text-first turns as terminal and the tool
  was never invoked. Replaced with a custom `streamHasToolCall`
  checker that scans the entire stream for any `ToolCalls` entry.
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
