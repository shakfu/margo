# Architecture

A contributor-facing tour of margo's code. This is the "where does
what live, and why" reference; for the user-facing taxonomy
(persona, agent, tool, workspace), see `docs/concepts.md`. For
strategic context (audience, platform thesis, prioritised
opportunities), see `REVIEW.md`.

This document is grounded in real file paths and is kept current
with the code; if you find a divergence, treat the code as
canonical and update this file.

## 1. Layered shape

```
                       pkg/margo (provider types, Client interface)
                                  ▲
                                  │
                       pkg/margo/{agent,rag,mcp}  (domain packages)
                                  ▲
                                  │
                       pkg/margo/core  (Session — UI-agnostic orchestration)
                       ▲          ▲          ▲
                       │          │          │
     cmd/margo (Wails)    cmd/margo-tui     cmd/margo-cli
     + frontend/Svelte    (Bubble Tea)      (one-shot)
```

Three rules govern this shape:

1. **Domain packages never import core or frontends.** Anyone can
   import `pkg/margo`, `pkg/margo/agent`, `pkg/margo/rag`,
   `pkg/margo/mcp`. They don't import each other except through
   the small `margo.Client` / `margo.Catalog` types in the root.
2. **`pkg/margo/core` is the only thing frontends should import.**
   Everything a frontend needs — provider routing, tool registry,
   workspace state, attachments, permissions, MCP — flows through
   `core.Session`. The TUI proves this works: 270 LoC, no Wails,
   no IPC — same library as the Wails app.
3. **Frontends never import each other.** Wails knows nothing about
   the TUI; the TUI knows nothing about Wails. Both link only
   against `pkg/margo/core`.

If a future change wants to violate one of these rules, that's a
sign the boundary needs revisiting. Open a discussion before
committing.

## 2. Package map

### Root: `pkg/margo`

The public-ish library shared by every other package.

- `client.go` — `Client` interface: `Name()`, `Complete()`,
  `Stream()`. Three implementations live under `providers/`.
- `models.go` + `models.json` — the embedded model catalog
  (`Catalog`, `Model`). Single source of truth for the model
  picker, context-window budgets, and the multimodal allowlist;
  frontends consume it via the Wails-bound `ModelsCatalog()`.
- `docs.go` — package-level doc; no runtime types.

Tests: `models_test.go`, `docs_test.go`.

### `pkg/margo/providers/{anthropic,openai,openrouter}`

Each provider is a thin adapter from `margo.Request` →
provider-native shape and provider stream → `margo.Chunk`. ~300
LoC each, similar layout:

- `Name()` returns the provider id (string keyed by `Session.clientFor`).
- `buildParams(req)` translates `margo.Request` into the SDK's
  request object — model, system prompt, sampling, thinking
  budget, tool definitions, stop sequences.
- `toAnthropicMessages` / `toSDKMessage` etc. translate
  `margo.Message` (with optional `Parts`) into multimodal content
  blocks.
- `Complete(ctx, req)` — non-streaming; returns `margo.Response`.
- `Stream(ctx, req)` — returns `<-chan margo.Chunk`. Tool-call
  arguments arrive in JSON fragments across multiple stream
  frames and are reassembled before emission.

Tests use `httptest.NewServer` + `option.WithBaseURL` to replay
fixtures without touching live APIs. The pattern is identical
across providers; see `pkg/margo/providers/anthropic/anthropic_test.go`
as the worked example.

### `pkg/margo/agent`

Eino integration and agent runners.

- `adapter.go` — wraps a `margo.Client` as
  `eino.components/model.ToolCallingChatModel` so the rest of
  the Eino runtime works against any of our providers.
- `runner.go` + `runner_{react,plan,workflow,adk}.go` — slash-
  command-selectable agent strategies:
  - **react** — ReAct loop (default; what plain chat uses with
    tools).
  - **plan** — plan-then-execute (planning phase + tool-using
    execution phase).
  - **workflow** — sequential drafter → critic → refiner
    pipeline; each stage is its own `adk.ChatModelAgent` with a
    distinct system prompt and tool palette.
  - **adk** — Eino ADK runner; lower-level access.
  `RunByType(runnerType, …)` is the dispatcher; empty string
  defaults to ReAct.
- `permission.go` — `WithPermissions` middleware. Wraps each
  tool's `InvokableRun` with a gate function; the gate emits a
  permission request and blocks on a `chan PermissionDecision`.
  `ReadOnlyTools` is a name allow-list of tools that bypass the
  gate (currently `current_time`, `search_knowledge`).
- `budget.go` — `BudgetForModel` reads the context window from
  `margo.DefaultCatalog`; `RewriteMargoForBudget` summarises old
  history when a conversation is about to overflow. Uses
  characters/4 as a coarse token estimate (acknowledged
  inaccurate by ~20-40% on dense content; a real tokenizer is
  on the roadmap).
- `tools_*.go` — built-in tools (`current_time`, `web_fetch`,
  `search_knowledge`, `quarto_render`). Each uses Eino's
  `toolutils.InferTool` to derive a JSON Schema from a Go struct.
- `stream.go` — `StepEvent` types (`StepText`, `StepToolCall`,
  `StepToolStream`, `StepRetrieve`, `StepToolResult`, `StepDone`,
  `StepError`) and the channel pumping.

Tests are thorough here — every runner, the budget, the
permission middleware, streaming, and each built-in tool has its
own `_test.go`.

### `pkg/margo/rag`

Workspace-scoped Retrieval Augmented Generation.

- `embedder.go` — `Embedder` interface; `embedder_openai.go`
  implements it via OpenAI's `/embeddings` endpoint. Requires
  `OPENAI_API_KEY` — the only RAG hard dependency on a paid API.
- `store.go` — `Store` interface; `store_chromem.go` implements
  it via chromem-go (embedded vector store, file-backed).
- `chunker.go` — text/markdown/PDF aware chunker. ~500 tokens
  per chunk with overlap.
- `loader.go` — turns a path (file or dir) into a list of
  chunks; supports PDF via `ledongthuc/pdf` and falls back to
  raw bytes for everything else.
- `indexer.go` — orchestrates loader → chunker → embedder →
  store. Tracks sources in a JSON sidecar (`sources.json`) so
  the UI can list and refresh them.

The `search_knowledge` tool in `pkg/margo/agent/tools_search_knowledge.go`
queries the active workspace's indexer.

### `pkg/margo/mcp`

Hand-rolled Model Context Protocol client. The platform thesis's
central bet.

- `protocol.go` — JSON-RPC 2.0 envelope, MCP message types
  (Initialize, ListTools, CallTool, content blocks).
- `client.go` — stdio client. Reads from an `io.ReadCloser`,
  writes to an `io.WriteCloser`; correlation by atomic int id,
  drain-on-crash for pending callers. Tested with `io.Pipe`-
  backed fake servers (`client_test.go`).
- `server.go` — `Server` wraps one subprocess: spawn via
  `exec.Cmd`, ring-buffered stderr tail, polite shutdown via
  stdin-close with 3s SIGKILL fallback.
- `manager.go` — named registry. Loads
  `<UserConfigDir>/Margo/mcp.json` (Claude-Desktop-compatible
  shape) and starts every server eagerly + async. `Tools()`
  returns aggregated `NamespacedTool` values
  (`mcp:<server>:<tool>`) from every Ready server.
- `adapter.go` — wraps each MCP tool as
  `eino.tool.InvokableTool` so the agent runner equips them
  like any builtin.
- `integration_test.go` (build tag `integration`) — spawns the
  real `@modelcontextprotocol/server-filesystem` via npx;
  validates the wire protocol end-to-end. Run via
  `make test-integration`.

### `pkg/margo/core`

The UI-agnostic orchestration layer. Frontends only see this
package.

- `session.go` — `Session` owns the provider clients, workspace
  registry, attachment store, permission broker, MCP manager,
  and the per-stream cancel registry. Streaming methods return
  `<-chan core.Event`:
  - `Stream(ctx, id, ChatRequest)` — plain chat. Emits
    `EventText` / `EventThinking` / `EventDone` / `EventError`.
  - `StreamAgent(ctx, id, AgentRequest)` — tool-using agent run.
    Adds `EventToolCall` / `EventToolStream` / `EventToolRetrieve`
    / `EventToolResult` / `EventPermission`.
- `types.go` — public API types (`Event`, `ChatRequest`,
  `AgentRequest`, `Message`, `Attachment`, `Options`, `Usage`,
  `Response`, `RetrievalHit`, `StoredAttachment`, …). JSON tags
  preserved so a Wails frontend gets stable shapes; a TUI
  ignores them.
- `tools.go` — built-in tool registry + MCP tool merging. Tool
  names with the `mcp:` prefix route through the manager; others
  resolve through the in-process `builtinTools` map.
- `workspace.go` — `WorkspaceRegistry` (per-workspace RAG
  indexers, lazy construction). The frontend pushes the active
  workspace id via `SetActive`; the `search_knowledge` tool reads
  it at invoke time.
- `attachments.go` — `AttachmentStore` over a configurable disk
  root. Bytes-in / bytes-out; base64 is a frontend concern (lives
  in `app.go` for Wails).
- `permission.go` — `PermissionBroker`. Transport-agnostic: the
  runner asks `New()` for an id + channel, the session emits a
  Permission event carrying that id, and the frontend later
  calls `Respond(id, …)` to resolve.
- `conv.go` — translation helpers between `core.Message` and
  `margo.Message` / `eino/schema.Message`. Internal.
- `export.go` — `RenderChatMarkdown` for the conversation-export
  feature. Pure function so both Wails and (future) TUI export
  share the renderer; the Wails edge owns the save-dialog +
  disk-write.

### `cmd/margo` (Wails desktop) and frontend

The default-shipped frontend.

- `main.go` — Wails boot. Parses the `-workspace` flag, loads
  MCP config from disk, embeds the Svelte build via
  `//go:embed all:frontend/dist`, runs `wails.Run` with
  `OnStartup`/`OnShutdown` hooks.
- `app.go` — Wails bindings. Pure wire-format adapter: JSON-
  tagged structs match the prior `wailsjs/go/main/App` shapes
  byte-for-byte; base64 ↔ bytes at the IPC edge; every
  streaming method consumes a `<-chan core.Event` from
  `Session` and translates to `runtime.EventsEmit` on
  `margo:stream:<id>:{chunk,done,error}` channels. ~417 lines
  after the refactor; was 1088.
- `frontend/` — Svelte 4 + TypeScript + Tailwind + Melt UI.
  - `src/App.svelte` — chat pane, message rendering, input,
    streaming event handler, chat list orchestration.
  - `src/lib/store.ts` — Svelte stores for settings, chats,
    workspaces. localStorage-backed; the canonical source for
    chat state today (a SQLite migration is a tracked future
    improvement — see REVIEW §7.6).
  - `src/lib/SettingsPanel.svelte` — thin tabs shell.
  - `src/lib/settings/` — focused subpanels:
    `ProviderSettings`, `PersonasSection`, `KnowledgeSection`,
    `ToolsSection`, `TrustedToolsSection`, `MCPServersSection`,
    `GeneralSettings`.
  - `src/lib/slash.ts` — slash-command parser
    (`/persona`, `/agent`, `/agent-plan`, …).
  - `src/lib/{ChatList,AttachmentThumb}.svelte` — other top-level
    components.

### `cmd/margo-tui` (Bubble Tea)

Self-contained TUI. ~270 LoC, no UI assets, no IPC.

- `main.go` — bootstrap: loads config + MCP config, builds
  `core.Session`, runs `tea.NewProgram` in alt-screen.
- `model.go` — Bubble Tea Model with `textinput` + `viewport`.
  The streaming pattern: each `core.Event` arrives as a
  `tea.Msg`; the read pump is a `tea.Cmd` that recursively
  schedules itself off `m.streamCh`. Agent events, permission
  prompts, attachments, multi-line input — all deliberately
  out of scope for the scaffold.

### `cmd/margo-cli`

Legacy one-shot prompt. 75 LoC; not a serious frontend, kept for
quick scripting.

## 3. Key flows

### 3.1 Streaming chat

User types in Wails → `App.StreamChat(id, ...)` → constructs a
`core.ChatRequest` → `Session.Stream(ctx, id, req)` returns
`<-chan core.Event` → goroutine in `app.go` ranges the channel
and translates each event to `runtime.EventsEmit` on
`margo:stream:<id>:{chunk,done,error}`.

```
Svelte                  Wails                   core.Session                 provider
  │ StreamChat(...)       │                            │                          │
  ├──────────────────────▶│                            │                          │
  │                       │ Stream(ctx, id, req)       │                          │
  │                       ├───────────────────────────▶│                          │
  │                       │                            │ clientFor(req.Provider)  │
  │                       │                            ├─────────────────────────▶│
  │                       │                            │ c.Stream(ctx, mreq) →    │
  │                       │                            │ <-chan margo.Chunk       │
  │                       │                            │◀─────────────────────────┤
  │                       │  <-chan core.Event         │                          │
  │                       │◀───────────────────────────┤   for chunk := range ch  │
  │ EventsOn("...:chunk") │                            │                          │
  │ EventsOn("...:done")  │                            │                          │
  │◀──────────────────────┤ runtime.EventsEmit(...)    │                          │
```

The TUI's version of the same flow is in `cmd/margo-tui/model.go`:
substitute the Wails event emit for a `tea.Msg` dispatch and
the rest is identical.

### 3.2 Agent run

`StreamAgent` is `Stream` plus a tool palette and a permission
broker.

```
App.StreamAgent(toolNames, autoApprove, runnerType)
  → Session.StreamAgent(ctx, id, AgentRequest)
    → Session.buildTools(toolNames)  // resolves builtins + mcp:* tools
    → agent.RunByType(runnerType, c, mreq, tools, ..., gate, emit)
      → react/plan/workflow runner loops:
        emit(StepText)
        emit(StepToolCall)
          gate(ctx, name, args) → blocks on PermissionBroker
            ← EventPermission to frontend
            ← Permissions().Respond(id, decision) from frontend
          → tool.InvokableRun(...)
        emit(StepToolResult)
        emit(StepDone)
```

The gate is the only thing that knows about the frontend. It
closes over a per-run `approvedThisRun` map so the user's
"Always" choices stick within the run; the frontend separately
persists "Trusted tools" to localStorage so they survive
restarts.

### 3.3 RAG retrieval

```
User enables search_knowledge for an agent →
  agent runner equips it via Session.buildTools(...) →
    search_knowledge closes over Session.workspaces.ActiveIndexer() →
      at call time, ActiveIndexer reads activeWorkspaceID and lazily
      constructs (or retrieves cached) rag.Indexer →
        Indexer.Search(query, k) → []rag.Hit →
          tool emits StepRetrieve event with the hits →
            frontend renders citation cards (App.svelte)
```

Workspace switching is implicit: the frontend calls
`Session.Workspaces().SetActive(id)` and subsequent
`search_knowledge` calls pick up the new indexer. There is no
per-tool registration step.

### 3.4 MCP tool invocation

```
Boot:
  mcp.LoadConfig(<UserConfigDir>/Margo/mcp.json) →
    Session.NewSession (eager+async start) →
      per-server goroutine: exec.Cmd → mcp.Client → Initialize →
        tools/list → cache on Server →
          Status: starting → ready

Run:
  agent runner picks an mcp:fs:read_file tool from buildTools →
    permission gate fires (same path as builtins) →
      InvokableRun → mcp.Manager.CallQualified →
        Server.CallTool → mcp.Client.CallTool → JSON-RPC tools/call →
          CallToolResult.Content → flattenContent → string →
            back to runner → emit StepToolResult
```

The frontend's MCP tab (`lib/settings/MCPServersSection.svelte`)
polls `App.MCPServers()` every 2 seconds while expanded — cheap,
avoids wiring a push channel for the MVP.

### 3.5 Attachment lifecycle

```
User drops a PNG in the chat:
  Svelte → SaveAttachment(chatID, name, mime, base64) →
    app.go: base64 decode → core.Attachments.Save(bytes) →
      <UserConfigDir>/Margo/attachments/<chatID>/<uniq>-<name> →
        return StoredAttachment{path, name, mime, size}

User sends:
  Svelte appends StoredAttachment to message.attachments →
    StreamChat/StreamAgent receives AttachmentInput (base64 again) →
      app.go: base64 decode → core.Attachment{bytes} →
        attachmentsToParts → margo.Part{PartImage|PartDocument} →
          provider-specific multimodal encoding (Anthropic source/base64,
          OpenAI image_url data URL, document text extraction for
          non-anthropic PDF)
```

Base64 is forced at the Wails IPC layer because JSON IPC can't
carry raw bytes cleanly. The TUI eventually skips base64 entirely.

## 4. Configuration

Three configuration surfaces, all loaded at boot:

| What | Where | Format | Hot-reload? |
|---|---|---|---|
| API keys | `.env` at repo root or process env | `KEY=value` | No (restart) |
| Model catalog | `pkg/margo/models.json` (embedded) | JSON | No (rebuild) |
| MCP servers | `<UserConfigDir>/Margo/mcp.json` | Claude-Desktop-shape JSON | No (restart) |

Configuration the user mutates at runtime — workspaces,
personas, agents, tools enabled, autoApprove, theme — lives in
localStorage under `margo:settings:v1` and `margo:chats:v1:<wsId>`.
This is the largest tech-debt risk in the project; see REVIEW
§3.1.1 and §7.6 for the planned SQLite migration.

## 5. Extension points

If you're adding to margo, you're probably doing one of:

### 5.1 Add a new tool

A "tool" is anything implementing `eino.tool.BaseTool`. If the
tool produces output incrementally, also implement
`eino.tool.StreamableTool`.

1. New file `pkg/margo/agent/tools_<name>.go` next to the
   existing builtins.
2. Use `toolutils.InferTool` (or `InferStreamTool`) to derive
   the JSON Schema from a Go struct with `jsonschema` tags.
3. Register in `pkg/margo/core/tools.go::builtinTools`. If the
   tool needs Session state (e.g. active workspace), the
   constructor takes `*Session`.
4. If the tool is read-only (idempotent, no side effects), add
   its name to `pkg/margo/agent/permission.go::ReadOnlyTools`
   so the gate auto-approves it.
5. Write `tools_<name>_test.go` covering the schema, happy
   path, and any error modes.

### 5.2 Add a new provider

1. New package `pkg/margo/providers/<name>/<name>.go`.
2. Implement `margo.Client`: `Name()`, `Complete(ctx, req)`,
   `Stream(ctx, req)`.
3. Add a `clientFor` case in `pkg/margo/core/session.go` and a
   field on `Session`.
4. Add entries to `pkg/margo/models.json` for the models you
   expose (id + contextTokens + multimodal).
5. Add an env-var case in `internal/config/config.go` and to
   `core.Config`.
6. Write `<name>_test.go` following `anthropic_test.go` /
   `openai_test.go` as templates — `httptest.NewServer` +
   `WithBaseURL` covers everything you need.

### 5.3 Add a new agent runner

1. New file `pkg/margo/agent/runner_<name>.go`.
2. Function with the signature used by the dispatcher in
   `runner.go::RunByType`.
3. Register in `RunByType`'s switch.
4. Add a slash-command alias in `frontend/src/lib/slash.ts` if
   the user needs a way to pick it.

### 5.4 Add a new frontend

This is the question the core extraction was built to answer.
The TUI is the worked example. Three things:

1. New `cmd/margo-<name>/main.go`. Construct a `core.Session`
   from a `core.Config` populated from env + (optionally)
   `mcp.json`.
2. Translate `<-chan core.Event` into your transport's idiom.
   - Wails: `runtime.EventsEmit`.
   - Bubble Tea: `tea.Msg`.
   - HTTP/SSE: write each event as a JSON SSE frame.
3. Implement the permission flow: render
   `EventPermission` somehow; call
   `session.Permissions().Respond(id, decision)` on user input.

That's the entire frontend contract. Everything else
(workspaces, RAG, attachments, MCP) is plumbed through
`Session` methods you already have.

### 5.5 Add an MCP server (user-facing, not code)

Edit `<UserConfigDir>/Margo/mcp.json`:

```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path"]
    }
  }
}
```

Restart margo. The server appears in the MCP tab of the right
sidebar (status: starting → ready). Its tools surface in the
Tools tab prefixed `mcp:filesystem:`; enable them on the agents
that need them.

## 6. Test landscape

- `make test` — runs everything default. ~14 packages, ~80 tests.
  Includes provider tests (anthropic / openai / openrouter) via
  `httptest`; covers the SSE / tool-call / multimodal paths.
- `make test-integration` — adds the MCP integration tests
  (build tag `integration`). Requires `npx` on PATH; downloads
  the npm package on first run (slow). Excluded from
  `make test` because the cold-start cost is unfriendly to
  inner-loop development.

Coverage as of this writing: thorough in `pkg/margo/agent`,
`pkg/margo/rag`, `pkg/margo/mcp`, `pkg/margo/providers`;
moderate in `pkg/margo/core`; minimal in `pkg/margo`;
**zero in `frontend/`** — see REVIEW §7.5 for the planned
Vitest + Playwright work.

## 7. Build / run

- `make dev` — Wails desktop in dev mode (hot reload).
- `make build` — Wails production build to `build/bin/margo.app`
  (macOS) or `build/bin/margo` elsewhere.
- `make tui` / `make tui-run` — Bubble Tea TUI.
- `make cli` / `make cli-run ARGS="-prompt hi"` — one-shot CLI.
- `make bindings` — regenerate `frontend/wailsjs/go/main/App.{js,d.ts}`
  after editing `app.go`'s exported method surface.
- `make frontend-dev` — Vite dev server standalone (no Wails)
  for UI-only iteration; Wails methods won't work but layout
  changes are fast.

API keys go in `.env` at the repo root. Required for the
provider you intend to use; the rest are optional. See
`.env.example`.

## 8. What this document does not cover

- The user-facing taxonomy (persona vs agent vs tool vs
  workspace). See `docs/concepts.md`.
- Strategic direction, audience question, prioritised
  refactors. See `REVIEW.md`.
- Per-package design rationale (e.g. why hand-roll MCP rather
  than wrap a third-party SDK). See `REVIEW.md` and the
  package-level Go docs.
- Slash command catalog. See `frontend/src/lib/slash.ts`.

When in doubt: read the code. Every package has a
package-level doc comment that summarises its purpose, and
public types carry doc comments explaining when and why to use
them. The Go side of margo is meant to be read.
