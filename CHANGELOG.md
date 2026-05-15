# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.1]

### Added

- Explicit "Default" row in the sidebar Personas list. Represents
  "no persona — the built-in 'assistant' voice"; sits at the top of
  the list with a description, an `ACTIVE` badge when it's the
  current workspace default, and a "Set default" button when it
  isn't. Clicking the row's button is the symmetric way to revert
  the workspace default to plain-assistant mode — the previous
  one-off "Clear" button next to "Default: <name>" has been
  retired in favour of the uniform pick-a-row pattern. Reverting a
  single chat to the default voice continues to be `/persona` with
  no argument (or `/default`), which clears the chat's persona
  binding so the bubbles read "ASSISTANT" again. `docs/concepts.md`
  updated to describe both revert paths and the new Default row.
- Persona scope split: workspace default vs per-chat override.
  New `Workspace.defaultPersonaId?: string` field; new chats in a
  workspace are seeded with that persona via `newChat()` (per-chat
  `/persona <slug>` continues to override stickily). The
  SettingsPanel → Roles → Personas section grows a "Set default"
  button per row and a "Default: <name>" / "Clear" indicator at
  the top of the list; the row tinted with the bubble-user
  background marks the active workspace default. Slash command
  semantics unchanged. `loadSettings` clears any
  `workspace.defaultPersonaId` pointing at a missing persona on
  load (idempotent).
- Dynamic assistant-message label. The "ASSISTANT" header above
  each assistant bubble now reads the active persona's name in
  uppercase when one is set (e.g. "EDITOR", "CODE REVIEWER"),
  fall back to "ASSISTANT" otherwise. Driven by a reactive
  `activePersona` derived from the active chat's `personaId`.
- Chat-header role picker retired. The `<select>` dropdown that
  conditionally appeared in the chat header has been removed
  entirely — persona discovery + selection is now the Roles tab
  in the right sidebar (for workspace defaults) and the
  `/persona <slug>` slash command (for per-chat overrides). The
  unused CSS, the `rolePickerValue` / `onRoleChange` helpers,
  and the `visiblePersonas` import in `App.svelte` all go.
  Removes the pre-existing UX bug where the picker was gated
  behind `{#if $activeChat}` and therefore invisible until the
  first message landed.
- Sequential workflow runner (TODO §9.6). New
  `pkg/margo/agent/workflow_runner.go` wraps
  `adk.NewSequentialAgent` with a three-stage **drafter → critic
  → refiner** pipeline. Each stage is a separate
  `adk.ChatModelAgent` with its own system prompt, all sharing
  the margo Adapter. Tool palette policy per stage: drafter gets
  the workspace's full enabled set (research / retrieval is most
  useful at the first pass), critic and refiner get no tools (the
  prompts forbid tool calls and removing them prevents the model
  from being tempted). AgentEvent → StepEvent translation reuses
  `bridgeAgentEvent`, so tool calls from the drafter render in
  the UI as standard `StepToolCall` / `StepToolResult` /
  `StepToolStream` events. Registered as `RunnerWorkflow`
  ("workflow"); backs `/agent-workflow <task>`. Sub-agent prompts
  are domain-neutral; §8.3 will re-point this runner at a
  user-configurable sub-agent chain without changing the
  assembly shape. The slash-command catalog
  (`SLASH_COMMANDS` in `frontend/src/lib/slash.ts`) loses its
  "not yet available" hints on both `/agent-plan` and
  `/agent-workflow` — both are now live. Coverage:
  `workflow_runner_test.go` confirms three-sub-agent assembly +
  prompt ctx cancel; `runner_test.go::TestLookupRunnerWorkflow`
  guards the registry binding.
- Plan-then-execute runner (TODO §9.5; collapses §6.5). New
  `pkg/margo/agent/plan_runner.go` wraps Eino's
  `adk/prebuilt/planexecute.New` with three sub-agents
  constructed in-place (`NewPlanner` / `NewExecutor` /
  `NewReplanner`), each pointed at the same `agent.Adapter` —
  our adapter is a `model.ToolCallingChatModel`, so the kit
  calls `WithTools` per-role to bind whichever tools that role
  needs. The executor receives the workspace's enabled tool
  palette plus the existing permission + abort-on-ctx-cancel
  middlewares; the planner and replanner do not, since they
  emit structured PlanTool / RespondTool calls only. The
  AgentEvent → StepEvent bridge is shared with the React
  runner (`bridgeAgentEvent`), so plan-execute runs surface
  tool calls and results in the UI uniformly with ReAct.
  Registered as `RunnerPlan` ("plan"); the slash parser routes
  `/agent-plan <task>` to this runner. MaxIterations capped at
  10 (matches the kit default; named explicitly at the call
  site). Coverage: `plan_runner_test.go` confirms the
  three-sub-agent assembly compiles and that ctx cancellation
  propagates promptly; `runner_test.go::TestLookupRunnerPlan`
  guards the registry binding. End-to-end behaviour against a
  real model is manual verification — scripting a complete
  planexecute protocol (PlanTool → ExecutedStep accumulation
  → RespondTool termination) would duplicate the kit's own
  prebuilt tests rather than testing our wrapper.
- Adopt Eino ADK as the runner substrate (TODO §9.4b). The ReAct
  loop now runs on `cloudwego/eino/adk` rather than the legacy
  `flow/agent/react` package. New `pkg/margo/agent/adk_runner.go`
  stands up an `adk.ChatModelAgent` wired to our existing
  `agent.Adapter`, runs it via `adk.Runner` with streaming enabled,
  and bridges `adk.AgentEvent` → `agent.StepEvent` so the Wails
  surface and frontend continue consuming the same event contract.
  The bridge handles both non-streaming (one fully-formed message
  per event) and streaming (MessageStream of chunks) variants,
  emitting `StepText` / `StepToolCall` for assistant messages and
  `StepToolStream` / `StepToolResult` for tool messages. The
  permission gate and abort-on-ctx-cancel middlewares plug into
  `adk.ToolsConfig.ToolsNodeConfig.ToolCallMiddlewares` unchanged
  (both are `compose.ToolMiddleware`s, which ADK accepts directly).
  The §6.3 budget rewriter moved from
  `react.AgentConfig.MessageRewriter` to an `adk.AgentMiddleware`
  with a `BeforeChatModel` hook — algorithm in `RewriteForBudget`
  unchanged; only the wiring point changed. `StreamReact` survives
  as a thin compatibility wrapper (`return ReactRunner{}.Run(...)`)
  so direct callers keep working. Deletions: custom
  `StreamToolCallChecker` (`streamHasToolCall`), the
  `ModelCallbackHandler.OnEndWithStreamOutput` mid-loop streaming
  workaround, the `ToolCallbackHandler` callbacks, and the
  `budgetRewriter` factory — all superseded by ADK's native
  ChatModelAgent. Coverage: the existing `stream_test.go` cases
  (mid-loop text ordering, cancel-mid-tool, streamable-tool stream
  events) plus the `runner_test.go` smoke test all pass against
  the new substrate without modification — the parity-verification
  test file added during §9.4b.1 was retired once the swap landed
  since it duplicated `stream_test.go` post-swap. Permission and
  budget test suites pass unchanged. The optional §9.4b.4 devops
  add-on (`eino-ext/devops` Mermaid + debug UI) is deferred.
- Retire bundled Agent records (TODO §9.4, Option A). With slash
  commands (§9.2) supplying the runner and the Tools tab (§9.3)
  supplying the palette, the `Agent` record's job — bundling persona
  + tool allowlist — no longer carries weight. Decision recorded
  in `docs/concepts.md`: persona shapes voice, agent is a per-turn
  runner picked via `/agent`, and tools are workspace-enabled. The
  former built-in agents ("Quarto Author", "Time-aware assistant")
  are **dropped entirely** — they were tool-directives wearing
  persona costumes, not voice configurations, and don't earn a slot
  in `BUILTIN_PERSONAS` under the concepts-doc definition.
  Discoverability for the underlying tools lives on the Tools tab
  ("`quarto_render`: drafts and renders Quarto documents"), where
  users will already be when they enable tools.
  `LEGACY_BUILTIN_AGENT_IDS` records the closed set of retired
  builtin ids so `migrateAgentIdsToPersonaIds` can clear them
  cleanly rather than leaving dangling `personaId` references; user-
  created agents still migrate to personas (id preserved, name +
  description + systemPrompt carried, tool allowlist surfaced as a
  hint pointing at the Tools tab). `loadChatsForWorkspace` rewrites
  every legacy `chat.agentId` it finds — either translating to
  `personaId` (user agents) or clearing (builtins) — and drops the
  legacy field. Both migrations are idempotent. UI surface changes:
  the role picker dropdown's "Agents" optgroup is gone (Personas
  only); the Settings → Roles tab loses its Agents section and the
  per-agent Edit / Duplicate / Delete dialog; the composer footer
  no longer shows the active-agent tool list; the legacy
  `agentMode` shim in `send()` is retired. The `Agent` interface,
  `Settings.agents` field, and `Chat.agentId` field stay declared
  (and the helper functions stay exported) so legacy localStorage
  payloads deserialise cleanly; they're scheduled for full removal
  in a follow-up slice once the migration has flushed real-world
  data. Settings tab renamed from "Agents" to "Roles" since it now
  contains personas, knowledge sources, tools, and trusted tools
  rather than the bundle records.
- Tools tab + per-workspace tool enablement (TODO §9.3). New
  Wails surface: `ToolMetadata` struct (`name`, `description`,
  `isReadOnly`, `isStreamable`) plus `App.ToolsMetadata()` that
  constructs each registered tool once to read its
  `tool.BaseTool.Info(ctx)`, look up the read-only flag in
  `agent.ReadOnlyTools`, and type-assert for
  `tool.StreamableTool`. Sorted by name for deterministic UI.
  `Workspace.enabledTools?: string[]` is the new per-workspace
  filter the resolver runs after the existing
  `agent.tools ∩ availableTools` intersection. Undefined
  `enabledTools` means "all enabled" so existing chats and brand-
  new workspaces behave exactly as before until the user narrows
  the palette — no surprise on upgrade. `setWorkspaceToolEnabled`
  seeds from the registered set on first toggle so unchecking one
  tool never silently disables every other tool by side effect;
  `isToolEnabledForWorkspace` is the single read path the
  resolver and the UI share. New "Tools" collapsible section in
  `SettingsPanel` (workspace mode only) renders the catalog with
  per-row checkbox, description, and read-only / streamable
  badges. Coverage: `tools_metadata_test.go::TestToolsMetadataShape`
  (current_time read-only + invokable, web_fetch streamable +
  network) and `TestToolsMetadataSortedByName` (deterministic
  order).
- Slash-command activation (TODO §9.2). New
  `frontend/src/lib/slash.ts` exports `parseSlash(input)` which
  recognises the four-command grammar from `docs/concepts.md`:
  `/agent <task>`, `/agent-<type> <task>`, `/persona <slug>` (with
  the bare form clearing the persona), and `/default` / `/clear`.
  Disambiguation rule honoured: the first token must match
  `^/[a-zA-Z][a-zA-Z0-9_-]*$`, so literal-slash text like
  `/etc/passwd is sensitive` falls through as plain text, while
  typos like `/agnet` surface as `{ kind: 'unknown' }` and become
  an inline error rather than getting silently shipped to the
  model. `send()` in `App.svelte` runs the parser first: `persona` /
  `clear` results update the chat state and return without sending a
  turn; `agent` results force the StreamAgent route with the picked
  runner type and the chat's persona supplying voice (the bundled
  `Chat.agentId`, if any, is intentionally ignored for that turn).
  Wails `StreamAgent` gains a final `runnerType string` parameter
  that flows through to `agent.RunByType` from §9.1; empty string
  defaults to ReAct, so the legacy role-picker path stays
  unchanged. Composer placeholder updates to "Send a message, or
  type `/` for commands…"; a small dropdown above the textarea
  surfaces matching `SLASH_COMMANDS` while the user types `/` (and
  switches to persona-slug suggestions once they've typed
  `/persona `). Coverage: hand-runnable
  `frontend/src/lib/slash.test.ts` exercises every grammar branch
  (bare commands, typed-runner variants, multi-line task,
  case insensitivity, whitespace tolerance, literal-path
  passthrough, misspelt → unknown, `slugify`); follow-up to wire
  vitest noted under TODO §9.2.
- Runner interface foundation (TODO §9.1). New
  `pkg/margo/agent/runner.go` introduces the `Runner` interface that
  every agent control loop must satisfy, a `ReactRunner` that wraps
  the existing `StreamReact` unchanged, and a registry
  (`RegisterRunner` / `LookupRunner` / `AvailableRunners` /
  `RunByType`) keyed by `RunnerType` string. `app.go::StreamAgent`
  now dispatches via `agent.RunByType(ctx, agent.RunnerReact, …)`
  rather than calling `StreamReact` directly, so future slices
  (§9.2's slash parser, §9.5's plan-execute runner, §9.6's workflow
  runner) can swap runner types per turn without touching the
  surrounding event plumbing or the Wails surface. Behaviour
  unchanged for the ReAct path; existing tests and call sites
  continue to use `StreamReact` directly. Coverage:
  `pkg/margo/agent/runner_test.go` — known/unknown/empty name
  resolution, default-to-react fallback, registry mutation
  (RegisterRunner panics on empty name or nil factory), a smoke
  test that `RunByType("react", …)` produces the same step
  subsequence as `StreamReact`, and a compile-time guard
  (`var _ Runner = ReactRunner{}`) so signature drift fails at
  build time rather than at dispatch.
- Document attachments (TODO §7.5). PDFs now ride the same attachment
  path as images: composer accepts `application/pdf` with a 25 MB cap;
  pending non-image attachments render as a filename badge instead of
  a thumbnail; the multimodal-model gate (§7.3) now only fires for
  *image* attachments, since documents reach the model without
  needing vision capability. Provider wiring: `margo.Part` gains a
  `Name` field so the extracted-text wrapper can attribute content to
  a specific attachment. Anthropic emits `NewDocumentBlock` with
  `Base64PDFSourceParam` for `application/pdf` — native PDF support.
  OpenAI and OpenRouter share a Go-side fallback: new
  `pkg/margo/docs.go::ExtractTextFromDocument` decodes PDFs via
  `github.com/ledongthuc/pdf` (and reads `text/*` as-is), trims to
  `MaxExtractedDocChars` (~100 KB so a 500-page book doesn't blow a
  context window), wraps the body in `<file name="...">` so the model
  can distinguish multiple attachments, and inlines as a text content
  part. Extraction failure surfaces as a clear marker inside the
  wrapper rather than silently dropping the attachment. `app.go`'s
  `attachmentsToParts` and `applyAttachments` route by MIME prefix
  (`image/*` → `PartImage`; everything else → `PartDocument`).
  Coverage: `pkg/margo/docs_test.go` (text-mime passthrough,
  unsupported-mime error, empty-data error, oversized truncation,
  malformed-PDF error); `attachments_test.go::TestAttachmentsToPartsRoutesByMime`
  (wire-format routing).
- Attachment persistence (TODO §7.4). Attachments now survive a chat
  reload. New Wails methods on `*App`:
  - `SaveAttachment(chatID, name, mimeType, dataB64) -> StoredAttachment`
    decodes the base64 payload and writes it to
    `os.UserConfigDir()/Margo/attachments/<chatID>/<stamp>-<rnd>-<safe-name>`.
    The chat-id and filename are sanitised (alphanumerics + `-_` only on
    the chat id; `filepath.Base` + separator strip on the name), so the
    Wails surface cannot be coaxed into writing outside the attachments
    root.
  - `LoadAttachment(path) -> base64` reads bytes back for replay, with a
    `filepath.Rel` guard that rejects any path outside the attachments
    root — `LoadAttachment` is not a general-purpose file reader.
  - `DeleteChatAttachments(chatID)` removes the per-chat subdir; called
    from `ChatList.confirmDelete` when the user forgets a chat.
  Frontend `Message` interface gains `attachments?: StoredAttachment[]`;
  `attachmentCount` is retained as a legacy fallback for pre-§7.4
  messages. `send()` calls `SaveAttachment` for each pending attachment
  *before* persisting the message — a failed save aborts the send so
  half-attached messages never reach the chat log. New
  `AttachmentThumb.svelte` component lazily loads each stored
  attachment via `LoadAttachment` and renders an inline `<img>` for
  images or a clickable filename badge for documents (clicking either
  opens the original via `OpenPath`). Coverage: `attachments_test.go`
  (round-trip save/load, idempotent delete, path-traversal escape
  rejection, chat-id validation).
- RAG polish: structured retrieval events + mtime-aware re-index.
  New `StepRetrieve` step kind (`agent.RetrievalHit` payload) carrying
  per-hit path, doc, score, and a 240-char snippet. `StreamReact` now
  stashes the emit callback on the run context via `WithStepEmitter` /
  `PublishStep` so tools can surface auxiliary structured events
  alongside their normal text return; `search_knowledge` publishes the
  hit list before returning its model-facing text. Wails routes a
  `tool_retrieve` chunk with a `hits[]` payload; frontend `AgentStep`
  gains `hits?` plus a `setStepHits` helper, and the step card now
  renders a clickable hit list (path · score · snippet) when present,
  falling back to the raw result text otherwise. `rag.Indexer` learns
  per-file mtime tracking: `SourceInfo.Files []IndexedFile{RelPath,
  ModTime, ChunkIDs}` is persisted in the sidecar, and `IndexPath`
  skips embedding for files whose mtime matches the prior run.
  `IndexResult` reports `SkippedFiles` / `EmbeddedFiles` so the UI
  can summarise the work; SettingsPanel grows a per-source "Refresh"
  button and a one-line status ("indexed …: N embedded, M unchanged").
  Coverage: `indexer_test.go::TestIndexerSkipsUnchangedFiles` (proves
  the embedder receives strictly fewer items on re-index);
  `tools_search_knowledge_test.go::TestSearchKnowledgePublishesHits`
  +`TestSearchKnowledgeNoEmitterIsHarmless`.
- Minimum-viable RAG (TODO §6.6.A end-to-end). New
  `pkg/margo/rag/indexer.go` composes the existing loader, chunker,
  embedder, and chromem-go store behind `Indexer.IndexPath`,
  `Indexer.Search`, `Indexer.Sources`, and `Indexer.DeleteSource`. A
  sidecar `sources.json` next to the chromem `.gob` tracks which
  paths the user has indexed (with their chunk ids) so re-indexing
  prunes stale chunks and DeleteSource cleans up by source rather
  than by chunk. New built-in tool `agent.SearchKnowledgeTool` wired
  into `builtinTools` as `search_knowledge(query, k=5)`; marked
  read-only so it skips the permission prompt. The tool ctor takes a
  `SearchProvider` interface (the narrow subset of `*rag.Indexer` it
  needs) so it can be unit-tested without an embedder. `builtinTools`
  now stores `func(*App) tool.BaseTool` constructors so tools can
  close over per-run state (currently: the active workspace's
  indexer). New Wails methods: `SetActiveWorkspace(id)` (pushed by
  the frontend on workspace switch), `IndexPath(wsID, path)`,
  `KnowledgeSources(wsID)`, `DeleteKnowledgeSource(wsID, path)`,
  `PickKnowledgePath(dirOnly)`. Per-workspace indexers are
  lazy-constructed and cached on the `*App`. Embedder is OpenAI
  `text-embedding-3-small` (1536 dims); indexing requires
  `OPENAI_API_KEY` and errors clearly otherwise. Frontend
  `SettingsPanel` (workspace mode) grows a "Knowledge sources"
  collapsible section listing indexed paths with per-entry Remove
  and "+ Index file" / "+ Index folder" buttons. Coverage:
  `indexer_test.go` (file + dir indexing, re-index replacement,
  sidecar persistence across re-open, DeleteSource);
  `tools_search_knowledge_test.go` (format, default k, nil provider,
  empty results). The previously unused `chromem-go` dependency is
  now a live dep.
- Streaming tools (TODO §6.4). `pkg/margo/agent` now surfaces a new
  `StepToolStream` step kind; `StreamReact` wires
  `cbtmpl.ToolCallbackHandler.OnEndWithStreamOutput` to drain a
  `tool.StreamableTool`'s output chunk-by-chunk, emitting each chunk as
  `StepToolStream` and a final concatenated `StepToolResult` so the
  existing UI merge logic still attaches a "final result" to the
  matching tool_call. First streamable tool: `web_fetch(url,
  max_bytes?)` — GETs an http(s) URL, rejects non-text content types
  and >=400 responses, reduces HTML to readable text (script / style
  blocks dropped, tags stripped, common entities decoded), caps the
  body at 256 KB by default, and streams the result in 4 KB chunks.
  Wails `AgentStepEvent` gains a `chunk` field; frontend `AgentStep`
  gains a `stream?` buffer with a new `appendStepStream` helper, and
  the step card grows a live monospace region that renders until the
  paired `tool_result` arrives. Coverage:
  `stream_test.go::TestStreamReactStreamableTool` exercises ordering
  and concatenation; `tools_webfetch_test.go` covers HTML reduction,
  multi-chunk plaintext streaming, truncation, and rejection of
  binary / non-http / 4xx responses.
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

- Role picker still listed "Quarto Author" and "Time-aware
  assistant" after §9.4's cleanup pass. Migration sediment: an
  interim build of §9.4 briefly placed those two records in
  `BUILTIN_PERSONAS`; users who booted on that build had them
  written into `margo:settings:v1`. When the cleanup pass
  removed them from `BUILTIN_PERSONAS`, the load-time filter
  `userPersonas.filter(p => !builtinPersonaIds.has(p.id))`
  preserved them as "custom" personas (their ids no longer
  matched any builtin). `loadSettings` now additionally filters
  through `LEGACY_BUILTIN_AGENT_IDS` so those ids are dropped
  on load, and `migrateAgentIdsToPersonaIds` picks up a third
  case — clearing any `chat.personaId` that still points at one
  of the retired ids so the binding doesn't dangle. Both passes
  are idempotent; data flushes on first boot under this build
  and subsequent boots are no-ops.
- Left sidebar collapse in `App.svelte` collapsed the main content
  area instead of the sidebar. The root grid declares three columns
  (`[280px_1fr_320px]` and zero-width variants), but the left/right
  `<aside>`s were wrapped in `{#if showLeft}` / `{#if showRight}`,
  so removing the left aside shifted `<main>` into the now-zero-width
  first column. The asides are now always rendered (with the
  `ChatList` / `SettingsPanel` contents gated by the `{#if}` and
  `aria-hidden` reflecting the collapsed state), so DOM children
  always match the grid template and the correct column collapses.
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

## [0.1.0]
