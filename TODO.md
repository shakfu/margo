# TODO

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

## 6. Eino integration — incremental adoption

We adopted Eino as a hard dependency (~10MB binary, ~117 transitive
packages) but only use ~10% of its surface (the `ToolCallingChatModel`
adapter pattern + the pre-built ReAct loop). To justify the dep weight,
work through the items below in order. Each subitem is independently
shippable; treat the ordering as a recommendation, not a hard chain.

### 6.3 Context-window management — follow-ups

Drop-oldest variant shipped (see CHANGELOG). Deferred:
summarisation-instead-of-drop (would preserve information but adds a
model call per iteration); injecting ephemeral system reminders
(waits on a per-chat preferences store that doesn't exist yet).

### 6.4 Streaming tools — **done**

Shipped (see CHANGELOG): `StepToolStream` event kind,
`OnEndWithStreamOutput` wiring in `StreamReact`, `web_fetch` as the
first streamable tool, and a live monospace region in the step card.
Follow-ups when demand surfaces: additional streamable tools
(`run_shell_command` would gate behind permissions; `tail_log` once
file-reading tools land), and richer rendering (e.g. ANSI colour for
shell output).

### 6.5 Custom graphs (`compose.Graph`)

This is Eino's actual value prop and where we currently get nothing.
First custom graph worth building: **plan-then-execute**. A planner
node generates a structured task list, a worker node executes each
step with tools, a reducer node summarises. Demonstrate by replacing
the single ReAct loop with a graph for a specific user-visible feature
(e.g. "summarise this PDF", "refactor this file"). Once one graph is in
place, additional workflows are cheap.

Pointers: `compose.NewGraph[I, O]()`, `AddChatModelNode`, `AddToolsNode`,
`AddLambdaNode`, `AddBranch`, `Compile`. The
`docs/dev/agents_and_tools.md` Pattern 2 sketch is the starting point.

### 6.6 RAG: embedding + indexer + retriever

Practical entry: "ask about this folder/file/PDF". Multi-week project —
scope each sub-bullet before starting and ship them as separate slices.

**Architecture.** Define a margo-side abstraction so storage backends
swap cleanly:

- `pkg/margo/rag/` — interfaces + glue.
- An `eino/components/embedding.Embedder` implementation wrapping an
  embedding model.
- `eino/components/indexer.Indexer` and `retriever.Retriever`
  implementations parameterised by a `VectorStore` interface (`Upsert`,
  `Query`, `Delete`, `List`).
- Two `VectorStore` backends:
  1. `pkg/margo/rag/store/chromem` — local default, see 6.6.A.
  2. `pkg/margo/rag/store/qdrant` — remote/distributed, see 6.6.B.
- Backend selection lives in `Settings` (per-app or per-collection); UI
  gets a backend picker in the knowledge-sources panel.

**Embedder.** Start with OpenAI `text-embedding-3-small` via
`eino-ext/components/embedding/openai` (or a thin hand-rolled wrapper
on the existing OpenAI SDK). Pluggable for Ollama (`mxbai-embed-large`,
`nomic-embed-text`) and Anthropic Voyage later. The `Embedder`
interface is shared across backends so the same vectors work in either
store.

**Document loader.** `eino-ext` has PDF / web / markdown loaders.
Chunking strategy: start with recursive-character (~800 tokens, 100
overlap) and revisit per content type.

**UI.** "Knowledge sources" right-pane section listing indexed
files/folders, with re-index controls and backend indicator (local
chromem-go vs. remote Qdrant). Step events get a new `StepRetrieve`
kind for showing which docs the agent pulled, with title + score +
clickable source.

**Tool integration.** Expose retrieval as a built-in agent tool
`search_knowledge(query, k=5)` registered in `app.go:builtinTools`.
ReAct can then opt-in to retrieval per query rather than forcing
every prompt through the retriever (which is the failure mode of
naive RAG).

#### Embedded backend selection

chromem-go (6.6.A) is the working default for the embedded slot. Bleve
(6.6.C) was evaluated and **ruled out**: its vector/KNN index relies
on `go-faiss` (CGo bindings to FAISS), which breaks margo's
zero-CGo property and complicates universal macOS Wails builds — the
same disqualifier that ruled out sqlite-vec. Bleve's pure-Go portion
covers BM25 / classic FTS only.

If hybrid retrieval (keyword + vector) turns out to be needed,
layer a small in-memory BM25 over chromem-go ourselves rather than
swap backends — see the note at the bottom of 6.6.C.

#### 6.6.A chromem-go — built-in embedded backend — **done**

Minimum-viable RAG shipped (see CHANGELOG): `rag.Indexer` composes
loader + chunker + OpenAI embedder + ChromemStore, with a sidecar
`sources.json` tracking indexed paths; `search_knowledge` tool wired
into `builtinTools`; per-workspace lazy-constructed indexers on
`*App`; Wails methods for index / list / delete / path-pick;
SettingsPanel knowledge-sources section.

Follow-ups for this backend:

- **50k-vector soft warning.** chromem-go loads the full HNSW index
  into RAM on open. Surface a soft warning in the panel when a
  workspace's chunk count crosses ~50k so the user can choose to
  switch to Qdrant (6.6.B) before performance degrades.

#### 6.6.B Qdrant — remote/distributed backend

For users with a corpus that exceeds chromem-go's ceiling, or who
want a shared index across machines/users. Qdrant runs as a separate
service (Docker, managed cloud, or self-hosted) and we talk to it
over its REST/gRPC API.

- **Dep.** `github.com/qdrant/go-client` (official Qdrant Go client,
  Apache-2.0). gRPC-based; brings in `google.golang.org/grpc` and
  protobuf if not already present — verify dep weight before
  committing.
- **Connection config.** New settings keys: `qdrantURL`,
  `qdrantAPIKey` (optional). Stored in `localStorage` like other
  settings; sensitive value lives only in the user's device.
  Optionally read from env (`QDRANT_URL`, `QDRANT_API_KEY`) so power
  users can provision via `.env` like the model API keys.
- **Implementation.** Wrap the Qdrant client to satisfy
  `rag.VectorStore`. Map our collection name 1:1 to Qdrant
  collection names; create on first upsert with the embedding model's
  dimension.
- **Connection health.** Probe on app start when Qdrant is the
  configured backend; surface a clear error in the knowledge-sources
  panel if unreachable rather than failing silently per-query.
- **Hybrid mode (later).** Some users will want local-first with
  remote sync. Defer until it's actually requested — premature
  generalisation otherwise.

#### 6.6.C bleve — evaluated and ruled out

Bleve is a mature pure-Go full-text search engine (BM25, custom
analyzers, multi-language) that added vector search via HNSW in
v2.4. **Vector search requires CGo** — the KNN index relies on
`go-faiss` bindings to FAISS. That disqualifies it from margo's
embedded slot for the same reason as sqlite-vec: CGo breaks
zero-CGo cross-builds and the universal macOS Wails packaging.

The pure-Go portion of bleve (BM25 / FTS only, no vectors) is still
shippable and could be combined with chromem-go for hybrid retrieval,
but at that point we're maintaining two embedded indices in lockstep
— a real complexity tax. A simpler alternative if hybrid turns out to
matter:

- **Custom in-memory BM25 over the same chunk corpus.** ~150 lines:
  tokenize on indexing, build inverted index, score with BM25
  (k1=1.5, b=0.75 defaults). Combine with chromem-go's vector scores
  via reciprocal rank fusion. Stays pure Go, single dep.
- **Defer the decision.** Build chromem-go-only first. Measure
  retrieval quality on a real corpus. Only add BM25/RRF if vector
  alone misses identifier-heavy queries (the most likely failure
  mode).

Reopen this option if `go-faiss` ever ships a pure-Go fallback or if
margo grows a corpus tier that justifies CGo (e.g. a "power user"
build flavour).

#### Implementation order

1. Define `pkg/margo/rag/` interfaces + the OpenAI embedder wrapper.
2. Land 6.6.A (chromem-go) end-to-end with one document type
   (markdown) and the `search_knowledge` tool — minimum viable RAG.
   Defer the hybrid-retrieval question until real-corpus quality
   data is in hand (see 6.6.C).
3. Add UI for managing collections and rendering retrieval steps.
4. Land 6.6.B (Qdrant) reusing the same interfaces and embedder.
5. Add remaining document types (PDF, web) to the loader pipeline.

### 6.7 Multi-agent (host + specialists)

`flow/agent/multiagent/host` lets a "host" agent route to specialist
agents (e.g. coder, researcher, summariser). Each specialist is itself
a ReAct agent with its own tool set. Useful when tool counts grow past
~10 and a single ReAct loop starts mis-selecting. Requires UI design
for nested agent activity (collapsible specialist sections in the
assistant bubble). Defer until tool count or task complexity demands
it.

### 6.8 Prompt templates (`components/prompt`)

Replace the raw system-prompt textarea with `prompt.FromMessages` +
`MessagesPlaceholder` for variable interpolation (Jinja2 via gonja —
already in our binary). Lower priority because the current free-form
system prompt works; only worth doing if we add structured per-chat
variables (user name, project context, role).

### 6.9 Native JSON Schema flow

`agent.Adapter.toolInfoToDef` round-trips schemas via JSON. Use
`schema.NewParamsOneOfByJSONSchema` and pass the schema through the
typed pathway instead. Tiny cleanup; do as part of any other
adapter work, not standalone.

### 6.10 Tool middleware — remaining slices

Permission prompts + trusted-tools UI shipped (see CHANGELOG).
Remaining cross-cutting middleware uses (not yet built; pursue when
demand surfaces):

- **Logging / tracing**: a middleware that logs invocation name +
  args + duration to a sink. Useful for debugging agent runs in
  production.
- **Rate limiting**: per-tool or per-key rate limits. Relevant once
  we have network-bound tools.

### 6.11 ToolReturnDirectly

`react.AgentConfig.ToolReturnDirectly` short-circuits the loop for
tools whose result IS the answer (e.g. `lookup_definition`). Saves a
model turn on those calls. Apply selectively per-tool when the
optimisation is worth it; not a generic improvement.

### 6.12 Enhanced (multi-modal) tools

`tool.EnhancedInvokableTool` returns `*schema.ToolResult` carrying
text, images, audio, video, files. Useful once we have tools that
produce non-text output (screenshot, generated chart, file
attachment). UI requires extending `AgentStep` and adding renderers
for each media type. Defer until we have a tool that needs it.

### 6.13 Eino ADK

The newer `adk/` package layers higher-level agent abstractions on
top of the components we're using. API is younger and may shift;
revisit after the items above land.

## 7. File / image attachments as input

The composer is text-only today. `ChatMessage.content` is a single string,
`margo.Message.Content` is `string`, no provider has multipart-message
plumbing. Adding attachments touches every layer (provider wire shapes,
Wails surface, frontend composer, persistence, token accounting), so the
work is sliced below in dependency order. Each slice is independently
shippable; ship in order.

### 7.1–7.3 — done

Provider multipart shape, image attachments end-to-end, and the
multimodal cross-provider gate all shipped (see CHANGELOG). Manual
verification of the OpenAI / OpenRouter vision paths against real
models is still worth doing.

### 7.4 Persistence + replay — **done**

Option B shipped (see CHANGELOG): attachments live under
`os.UserConfigDir()/Margo/attachments/<chatID>/`, the message records
only a `StoredAttachment` (path + name + mime + size), and the
thumbnail component reads bytes back on demand via `LoadAttachment`.
A future re-send path (not built yet — there's no UI for editing a
prior turn) would call `LoadAttachment` for each stored attachment
and feed it through the existing `AttachmentInput` plumbing
unchanged.

Follow-ups when demand surfaces:

- **In-memory thumbnail cache.** Today every render of a long chat
  re-reads each attachment from disk on mount. A small Map keyed by
  path inside the AttachmentThumb component (or hoisted to a store)
  would amortise re-renders. Cheap fix; defer until a chat with
  20+ attachments is in steady use.
- **Orphaned-blob GC.** If a chat is deleted while its
  `DeleteChatAttachments` call fails, the directory survives but the
  chat doesn't. A startup sweep that diffs on-disk chat dirs against
  the persisted chat list would clean those up.

### 7.5 Document (PDF / text) attachments — **done**

Shipped (see CHANGELOG): Anthropic native PDF blocks via
`NewDocumentBlock` + `Base64PDFSourceParam`; OpenAI / OpenRouter
text-extraction fallback in `pkg/margo/docs.go::ExtractTextFromDocument`
using `github.com/ledongthuc/pdf` for PDFs and passthrough for
`text/*`; composer accepts `application/pdf` at a 25 MB cap; the
§7.3 multimodal gate only fires for image attachments since
documents work on any model.

Follow-ups:

- **Token cost on the composer.** The 100 KB extracted-text cap is
  protective but invisible to the user. Surfacing an estimated
  "+N tokens" pip per pending attachment is part of §7.6.
- **Non-PDF docs.** `.docx`, `.html`, `.epub` would need additional
  extractors. Add when a user actually asks; the abstraction is in
  place (`ExtractTextFromDocument` returns "unsupported mime" for
  anything else today).

### 7.6 Attachment token accounting

The context-usage ring (`frontend/src/lib/store.ts::contextWindowFor`)
counts tokens-in / tokens-out from past completions, but
attached-but-unsent images / docs aren't counted yet. Approximate cost:

- **Images**: model-specific. Anthropic Claude charges by tile
  (`(width / 1568) * (height / 1568) * 1568 tiles → ~tokens`); OpenAI
  has a similar tile model. A coarse approximation is `width * height
  / 750` tokens per image, which is good enough for a UI hint.
- **Documents**: `len(extracted_text) / 4` chars-per-token heuristic
  (or run tiktoken if we want precision — see if #6.3's
  context-window work has already pulled in a tokenizer).

Surface a "+N tokens" pip on the composer's attachment thumbnails so
the user sees the cost before sending. Pairs naturally with #6.3 if
both land — both feed the same context budget.

## 8. Personas and Agents

Conceptual split: a **Persona** is a tool-less role (a packaged system
prompt — voice, expertise, output structure); an **Agent** is a
persona that also carries a tool allowlist (a curated capability that
runs through `StreamAgent`'s ReAct loop). Agents compose; personas
don't. See `docs/dev/personas_and_agents.md` for the design doc that
covers the data model, UX, sequencing, anti-patterns, and tradeoffs in
detail. The entries below mirror that doc's rollout sequence.

### 8.1–8.2 — done

Personas and Agents shipped (see CHANGELOG); design doc lives at
`docs/dev/personas_and_agents.md`.

### 8.3 Composition

After 8.1 / 8.2 see real use. Reuses the same data model with
`Agent.composedOf: string[]` populated. Two flavors:

- **Pipeline** (sequential): `A → output → B → output → user`.
- **Host / specialists** (hierarchical): host routes to A or B per
  request. Maps to Eino's `flow/agent/multiagent/host` (existing
  TODO #6.7 collapses into this).

This is also where TODO #6.5 (custom graphs / plan-then-execute)
lands naturally — a planner agent is one whose runner is a
`compose.Graph` instead of a ReAct loop. Discriminator field
(`composition: "pipeline" | "host"`) introduced in 8.3.

## 9. Slash-command activation model

Reshape the activation UX around four orthogonal concepts —
**persona** (voice), **agent** (runner), **tools** (palette), **model**
(provider + sampling) — as set out in `docs/concepts.md`. Today's
`Chat.agentId` bundles persona + tool allowlist + runner into one
record, which conflates these axes. The redesigned model:

- An **agent** is a *runner* (ReAct, plan-execute, workflow, …),
  invoked per turn via a slash command. There is no per-agent tool
  allowlist — the runner operates over whichever tools the user has
  enabled globally for the active workspace.
- A **persona** stays a per-chat voice configuration, activated via
  `/persona <name>` (or the sidebar Personas tab). Persistent for the
  chat until cleared.
- **Tools** become globally enable-able/disable-able per workspace
  via a new Tools tab. Registration at app startup is unchanged; the
  toggle is the user-controlled filter the runner sees.
- The chat-header role dropdown goes away. Active persona shows as a
  small chip in the header (clickable to clear); active agent appears
  as a transient badge on the in-flight turn's bubble and disappears
  when the turn ends.

Slash grammar:

| Command                  | Effect                                                         |
|--------------------------|----------------------------------------------------------------|
| `/agent <task>`          | Run the task one-shot through ReAct over enabled tools.        |
| `/agent-plan <task>`     | Same, plan-execute runner.                                     |
| `/agent-workflow <task>` | Same, workflow runner.                                         |
| `/persona <slug>`        | Persistent: set `Chat.personaId`. No task arg expected.        |
| `/persona`               | Clear the persona.                                             |
| `/default` / `/clear`    | Clear persona (no-op on agent — agents are already one-shot).  |

Parsing rule: only the first character matters AND it must be
followed by a recognised command word. Unknown `/foo` errors inline
("Unknown command. Did you mean `/agent`?") rather than silently
sending — keeps typos out of the model. Slash autocomplete combobox
fires on the leading `/` (Melt UI combobox, same affordance as
elsewhere in the app).

### 9.1 Runner interface (Go) — **done**

Shipped (see CHANGELOG). `pkg/margo/agent/runner.go` defines the
`Runner` interface, `ReactRunner` (wraps `StreamReact` unchanged),
and the registry (`RegisterRunner` / `LookupRunner` /
`AvailableRunners` / `RunByType`). `app.go::StreamAgent` dispatches
via `RunByType(ctx, RunnerReact, …)`. `StreamReact` stays as the
canonical implementation so existing call sites and tests work
unchanged. Future runners (§9.5 plan-execute, §9.6 workflow)
register a factory at init() and become reachable by name.

### 9.2 Slash parser + autocomplete (frontend) — **done**

Shipped (see CHANGELOG). `frontend/src/lib/slash.ts` exposes
`parseSlash`, `slugify`, and the `SLASH_COMMANDS` catalog;
`App.svelte::send()` dispatches on the parse result; Wails
`StreamAgent` carries `runnerType` through to `agent.RunByType`.
A simple autocomplete dropdown above the composer renders
matching `SLASH_COMMANDS` while the user is typing `/`.

Follow-ups when demand surfaces:

- **Wire a frontend test runner.** Today's `slash.test.ts` is
  hand-runnable via `npx tsx`; adding `vitest` to `frontend/`
  (≈5 lines in `package.json`) would let it run as part of CI.
  Deferred because it's a meta-decision affecting every future
  frontend slice and worth choosing deliberately.
- **Keyboard navigation in the autocomplete.** Arrow keys +
  Tab/Enter to insert the focused suggestion. Today the
  dropdown is click-to-insert only; the click target is large
  but keyboard-only users have to type the full command.
- **Argument suggestions for `/agent-<type>`.** Today only
  `/persona` gets argument suggestions (persona slugs); the
  runner-type completion for `/agent-` could list
  `AvailableRunners()` from §9.1's Go registry via a new Wails
  binding.
- **Role-picker dropdown retirement.** Lives on alongside slash
  commands today so users can discover roles by clicking.
  Removal lands with §9.4 (Retire bundled Agent records) since
  it's the same UI surface.

### 9.3 Tools tab + per-workspace tool enablement — **done**

Shipped (see CHANGELOG). New `App.ToolsMetadata` Wails method
exposes name / description / read-only / streamable per tool;
`Workspace.enabledTools?: string[]` carries the per-workspace
filter (undefined = all enabled); `SettingsPanel` (workspace
mode) grows a Tools section with toggles. `send()` applies a
two-stage filter: `agent.tools ∩ availableTools`, then
`∩ enabledTools` if defined.

Follow-ups when demand surfaces:

- **Registered-status greying.** The TODO entry asked for a
  greyed "not on PATH" indicator (e.g. quarto_render when
  `quarto` is missing). Today `builtinTools` only registers a
  tool when its host binary is present, so unregistered tools
  never appear in `ToolsMetadata` at all — the greying is
  unnecessary. Reopen if a future tool registers conditionally
  but should still surface as a discoverability hint.
- **Bulk enable / disable.** A "Disable all" / "Enable all"
  pair would be useful once workspaces routinely run a sub-set
  of tools. Defer until users actually narrow the palette in
  practice.

### 9.4 Retire bundled Agent records — **done** (Option A)

Shipped (see CHANGELOG). Decision: **Option A — retire entirely**.
Built-ins migrated to `BUILTIN_PERSONAS` with tool-pairing hints in
their system prompts; user-created agents migrated to personas at
load time (id preserved; tool allowlist surfaced as a hint).
`Chat.agentId` rewritten to `Chat.personaId` per-chat. Role picker
loses the Agents optgroup; Settings tab renamed to "Roles". Legacy
type declarations (`Agent`, `Settings.agents`, `Chat.agentId`) kept
declared so legacy localStorage deserialises cleanly; removal
follow-up below.

Follow-ups when demand surfaces:

- **Type cleanup.** After the migration has flushed real-world data
  (i.e. every active install has booted at least once on a
  post-§9.4 build), the `Agent` interface, `Settings.agents` field,
  `Chat.agentId` field, and unused store helpers (`setChatAgent`,
  `findAgent`, `visibleAgents`, `agentMissingTools`,
  `upsertAgent`, `deleteAgent`, `duplicateAgent`) can be deleted.
  Holding them now buys deserialisation safety at the cost of a
  bit of dead code.
- **Presets (Option B), if requested.** If users miss the one-click
  bundling, reintroduce as a UX-only shortcut: clicking a Preset
  sets the chat's persona AND writes a workspace-level enabledTools
  list. No runtime status, no new Wails surface — pure store
  mutation. Deferred unless asked.

### 9.4b Adopt Eino ADK as the runner substrate — **done**

Shipped (see CHANGELOG). ReactRunner now runs on
`adk.ChatModelAgent` + `adk.Runner`; `StreamReact` is a thin
compat wrapper. Permission middleware plugged into
`adk.ToolsConfig` unchanged (same `compose.ToolMiddleware` shape);
budget rewriter migrated to `adk.AgentMiddleware.BeforeChatModel`.
Legacy hand-rolled callback handlers, `streamHasToolCall`, and the
mid-loop streaming workaround retired — ADK's `ChatModelAgent`
handles them natively. Existing tests pass without modification
on the new substrate.

Follow-up when demand surfaces:

- **§9.4b.4 devops integration.** `eino-ext/devops.Init(ctx)`
  exposes a Mermaid + step-debug UI for compiled agents. Pulls in
  `chromedp` (heavy dep) — gate behind a build tag if adopted.
  Useful once §9.5 / §9.6 / §8.3 land and you want to inspect
  the runtime graph.

#### Original scoping notes

Substrate migration before §9.5 / §9.6 / §8.3. Eino's Agent
Development Kit (`cloudwego/eino/adk`) ships the exact agent shapes
the rest of §9 needs as prebuilt components, plus several
infrastructure features we've been hand-rolling (resume /
checkpoint, scoped state, unified callback handlers, agent
middleware). Adopting it *now* replaces a §9.5 / §9.6 / §8.3
implementation budget of "build the loop from compose.Graph
primitives" with "wire the prebuilt constructor to our slash
command."

What ADK provides that we'd otherwise build:

- **`adk.Agent` interface** — uniform abstraction with an
  `AsyncIterator[*AgentEvent]` output stream. Replaces ad-hoc
  step-event channels.
- **`adk.ChatModelAgent`** — the kit-level ReAct equivalent.
  Configured with name, description, instruction, model, tools.
  Drop-in replacement for our current `react.NewAgent` usage.
- **`adk.Runner`** — top-level executor with `Query` / `Run` /
  `Resume` and a first-party `CheckPointStore` for pausing /
  resuming runs (the substrate human-in-the-loop §6.4-style
  flows want).
- **Built-in compositions** — `NewSequentialAgent` (§9.6 in
  one constructor), `NewLoopAgent`, `NewParallelAgent`,
  `AgentWithDeterministicTransferTo` (deterministic sub-agent
  routing).
- **`prebuilt/planexecute`** — `planexecute.New(ctx,
  &planexecute.Config{Planner, Executor, Replanner,
  MaxIterations})`. §9.5 becomes a thin wrapper. The
  `Replanner` slot gives the "execute → re-plan → execute"
  loop that custom-built plan-execute wouldn't get for free.
- **`prebuilt/supervisor`** — multi-agent host with **unified
  tracing** (entire supervisor structure shares a single trace
  root). §8.3's "host" composition flavor.
- **`prebuilt/deep`** — deeper task orchestration with TODO
  session keys; future option for richer workflows.
- **`adk.AgentMiddleware`** — kit-level middleware analog to our
  current `compose.ToolMiddleware`. Permission gate (§6.10)
  migrates to this.
- **`adk.HistoryRewriter`** — what `pkg/margo/agent/budget.go`
  is, expressed at the kit interface. Budget code keeps its
  algorithm; just registers via this hook instead of
  `react.AgentConfig.MessageRewriter`.
- **`SessionValue` / `RunLocalValue`** — scoped state without
  hand-rolling context maps. Useful for the per-run
  search_knowledge indexer plumbing (§6.6.A) and future
  per-session state.

Non-trivial reframing this slice forces:

- **Public Runner interface stays** (`agent.Runner` from §9.1)
  as the margo-side facing type, but its `Run` body delegates
  to `adk.Runner.Run` and bridges `AgentEvent` →
  `StepEvent`. Shields callers from ADK API churn and leaves
  room for non-ADK runners later.
- **`StepEvent` stays** as the Wails-facing event shape — the
  frontend already consumes it. The translation layer becomes:
  `adk.AgentEvent` → `agent.StepEvent` → `app.AgentStepEvent`.
- **`StreamReact` becomes a thin wrapper** around an
  ADK-backed `ReactRunner`. Workarounds for eino-internal
  quirks (custom `StreamToolCallChecker`, mid-loop text
  streaming via `ModelCallbackHandler.OnEndWithStreamOutput`)
  retire if ADK handles them natively; verify per-case during
  the slice and only delete after the existing
  `stream_test.go` cases pass through the new path.
- **Permission middleware** (`agent/permission.go`) and the
  permission-prompt UI surface stay, but the gate moves from
  `compose.ToolMiddleware` to `adk.AgentMiddleware`.
  Behavioural contract — prompts only for non-read-only tools,
  Always-approve list persists — unchanged.

#### 9.4b.1 Adapter + ChatModelAgent bridge

Stand up an ADK-backed `ReactRunner` next to (not replacing)
the existing one, behind a feature flag. Verify
`agent.Adapter` plugs into `ChatModelAgent.Model` cleanly.
Implement the `AgentEvent → StepEvent` translation. Run every
existing `stream_test.go` case through both paths to confirm
behavioural parity; especially the mid-loop streaming + claude-
preamble-with-tool-call cases that today hinge on our custom
hooks.

#### 9.4b.2 Middleware migration

Move the permission gate from `compose.ToolMiddleware` to
`adk.AgentMiddleware`. Move budget rewriting from
`react.AgentConfig.MessageRewriter` to `adk.HistoryRewriter`.
Behavioural tests (`permission_test.go`, `budget_test.go`)
should pass without modification — they exercise the algorithm,
not the wiring.

#### 9.4b.3 Swap default + clean up

Flip the default `RunnerReact` factory to the ADK-backed
implementation. Delete the now-unused custom callback handlers,
custom `StreamToolCallChecker`, and any mid-loop streaming
plumbing that ADK supersedes. Keep `StreamReact` exported as a
thin wrapper so external callers (cmd/, tests) keep working.

#### 9.4b.4 Devops integration (optional follow-up)

`eino-ext/devops.Init(ctx)` starts an in-process HTTP server
that exposes a visualisation + debug UI for compiled graphs.
Pulls in `chromedp` (heavy dep) for the Mermaid renderer. Worth
gating behind a build tag (`-tags=devops`) so the standard
build stays lean; useful for development sessions on the
plan-execute and supervisor agents that follow.

### 9.5 Plan-execute runner — **done**

Shipped (see CHANGELOG). `pkg/margo/agent/plan_runner.go` wraps
`adk/prebuilt/planexecute.New` with planner / executor / replanner
sub-agents sharing the margo Adapter. Registered as `RunnerPlan`;
backs `/agent-plan <task>`. Collapses §6.5.

### 9.6 Workflow runner — **done**

Shipped (see CHANGELOG). `pkg/margo/agent/workflow_runner.go`
wraps `adk.NewSequentialAgent` with a hard-coded drafter →
critic → refiner pipeline. Registered as `RunnerWorkflow`;
backs `/agent-workflow <task>`. §8.3 will re-point this at a
user-configurable sub-agent chain without changing the
runner's assembly shape.

### Sequenced rollout

1. **§9.1** — Runner interface refactor. ReAct path unchanged
   behaviorally; surface for downstream slices.
2. **§9.2** — Slash parser + autocomplete, dispatching `/agent` to
   the ReAct runner. Coexists with the dropdown picker.
3. **§9.3** — Tools tab + per-workspace enablement. Retire the
   legacy "all tools always available" assumption.
4. **§9.4** — Retire bundled Agent records; decide A vs B. Drop the
   dropdown picker.
5. **§9.4b** — Adopt ADK as the runner substrate. Behavioural
   parity for the ReAct path; unlocks shrunk §9.5 / §9.6 /
   §8.3 surfaces.
6. **§9.5 / §9.6** — Add runners as prebuilt-constructor wrappers.
   Each is an independent slice on top of the ADK substrate.

### Tradeoffs to revisit during 9.4

- **Setup friction.** Today's "click Quarto Author" becomes "pick
  Quarto Author persona AND enable `quarto_render` tool". Two clicks
  vs one. Slash-command speed for power users is the compensating
  win; Presets (Option B) are the safety net if friction bites.
- **Discoverability of slash commands.** Casual users won't know
  they exist. Mitigation: the composer placeholder hint and the
  always-visible sidebar tabs as the discovery surface.
- **Workspace vs chat scope for tool enablement.** Per-workspace
  fits the rest of the workspace settings model and is the default.
  Per-chat is more granular but adds setup-per-chat friction; defer
  unless requested.

### Tradeoffs to revisit during 9.4b

- **ADK API stability.** TODO §6.13 originally flagged ADK as
  "younger and may shift". The size of the prebuilt catalog
  (`planexecute`, `supervisor`, `deep`) and the examples repo
  suggest it has stabilised, but pin the eino version and
  check the upstream CHANGELOG before committing. A breaking
  change after we delete the hand-rolled scaffolding has
  asymmetric cost.
- **Cancellation semantics.** Our `abortOnCtxCancel`
  middleware races slow tool calls against ctx.Done. ADK has
  its own `compose.WithGraphInterrupt` + `InterruptCtx` flow.
  Verify a `cancel()` from the UI mid-tool still returns
  promptly under the new substrate — `TestStreamReactCancelMidTool`
  is the existing guard; needs to pass through the ADK path
  before §9.4b.3 flips the default.
- **Event-shape contract.** `adk.AgentEvent` carries fields
  ours doesn't (e.g. transfer events between sub-agents) and
  vice versa. The translation layer must be exhaustive: any
  AgentEvent type we don't translate becomes a silent drop.
  Add a default branch in the translator that logs and surfaces
  an explicit "unhandled event kind" rather than failing
  quiet.
- **Dep weight.** ADK is already pulled in via the
  `eino@v0.8.13` dep (the import path is just a subdirectory).
  Adoption doesn't grow the binary. The `eino-ext/devops`
  optional add-on (chromedp) is the only new heavy dep — gate
  it behind a build tag.
