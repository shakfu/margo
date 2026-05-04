# TODO

## 1. Streaming markdown flicker — **done**

`renderMarkdownStreaming(text, streaming)` in
`frontend/src/lib/markdown.ts` throttles the parse rate to once per 50ms
while `streaming=true` (the in-flight assistant message), returning a
single-slot cached HTML between parses. When `streaming` flips to false
the cache is cleared and a fresh clean parse runs on the trailing edge
(driven by Svelte's reactive re-evaluation when `busy` changes). The
non-streaming case in `App.svelte` was always cheap and is unaffected.
(MathJax typesetting is already debounced 250ms in
`frontend/src/lib/mathjax.ts`, so it doesn't compound the per-chunk
cost.)

## 2. Link target — open in system browser — **done**

Implemented entirely on the frontend side (no Go-side navigation
interception needed):

- `frontend/src/lib/markdown.ts` registers a DOMPurify
  `afterSanitizeAttributes` hook that injects `target="_blank"` and
  `rel="noopener noreferrer"` on anchors with `http(s):` or `mailto:`
  hrefs.
- `frontend/src/App.svelte` adds a capture-phase document click handler
  that intercepts those same anchors, prevents the default navigation,
  and calls Wails' `BrowserOpenURL(href)` to open the URL in the
  user's system browser.

Internal `#fragment` and relative links are deliberately left alone.
**Manual verification required**: click a markdown link in the running
Wails app and confirm it opens in the default browser without replacing
the app shell.

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

## 4. Agent run cancellation race — **done** (see #6.2)

## 5. Mid-loop text streaming for agent runs

`agent.StreamReact` consumes `react.Agent.Stream`, which only streams the
*final* assistant answer. Intermediate model turns that produce both text and
tool calls (e.g. "Let me check the time first.") emit the tool calls visibly
through the callback path but the accompanying text is dropped. Fix:

- Replace (or augment) the `Stream` consumption with a
  `cbtmpl.ModelCallbackHandler{OnEndWithStreamOutput: ...}` registered
  alongside the existing `ToolCallbackHandler` in
  `pkg/margo/agent/stream.go`. The handler receives a
  `*schema.StreamReader[*model.CallbackOutput]` per model turn — drain it
  in a goroutine, emit each non-empty `Message.Content` delta as a
  `StepText` event, and remember to `Close()` the reader.
- Be careful about ordering: text deltas from turn N should land before
  the tool_call cards from the same turn. The callback fires on `OnEnd`,
  which is after the model has completed the turn, so this should be
  naturally correct, but worth verifying with a multi-turn test.

## 6. Eino integration — incremental adoption

We adopted Eino as a hard dependency (~10MB binary, ~117 transitive
packages) but only use ~10% of its surface (the `ToolCallingChatModel`
adapter pattern + the pre-built ReAct loop). To justify the dep weight,
work through the items below in order. Each subitem is independently
shippable; treat the ordering as a recommendation, not a hard chain.

Items 6.1 and 6.2 supersede tasks 5 and 4 respectively — they're listed
here in priority order with concrete Eino APIs to reach for.

### 6.1 Mid-loop text streaming (supersedes #5) — **done**

Implemented in `pkg/margo/agent/stream.go`: a
`utils/callbacks.ModelCallbackHandler.OnEndWithStreamOutput` drains each
intermediate model turn synchronously and emits its text as a single
`StepText` event before the turn's tool callbacks fire. Final-turn text
continues to flow through the outer agent stream; dedup is gated on
"this turn produced tool calls". Also added a custom
`StreamToolCallChecker` (`streamHasToolCall`) replacing eino's default
first-chunk checker, which misclassified text-first-then-tool-call turns
(typical for Claude) as terminal and silently broke tool invocation.
Coverage: `TestStreamReactMidLoopTextOrdering`. See
`docs/dev/agents_and_tools.md` § "Mid-loop text streaming" for the
final-turn dedup assumption.

### 6.2 Cancellation via interrupt (supersedes #4) — **done**

Investigated Eino's interrupt machinery
(`compose.WithInterruptBeforeNodes`, `core.InterruptSignal`,
`StatefulInterrupt`) and found it is designed for human-in-the-loop
checkpoint/resume, not for ctx cancellation — there is no graph-level
"abort now" signal that nodes poll between steps. The pragmatic fix is
the ctx-aware tool wrapper, registered as a `compose.ToolMiddleware`
on the ReAct ToolsNode rather than per-tool. See `abortOnCtxCancel`
in `pkg/margo/agent/stream.go`. UI shows "cancelling…" while the run
unwinds. Coverage: `TestStreamReactCancelMidTool`. Docs:
`docs/dev/agents_and_tools.md` § "Cancellation".

### 6.3 Context-window management via MessageRewriter

`react.AgentConfig.MessageRewriter` runs before each model call and can
rewrite the accumulated message history. Wire it to:

- Drop or summarise the oldest turns when token count approaches
  `contextWindowFor(model)` (we already track running totals in
  `Chat.tokensIn`/`tokensOut`).
- Optionally inject ephemeral system reminders ("the user's preferred
  language is X", from a future per-chat preferences store).

This is real value users feel — long conversations stop overflowing.

### 6.4 Streaming tools

Implement a real `tool.StreamableTool` (e.g. `web_fetch`,
`run_shell_command`, `tail_log`) and pipe its chunks back through the
existing step-event channel as a new `StepKind` (`StepToolStream` or
similar). UI: extend the step card in `App.svelte` to grow a streaming
result region. Pre-req for any agent that does I/O slower than ~1s.

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

#### 6.6.A chromem-go — built-in embedded backend

Pure Go, no CGo, in-process, persisted to the app's user-data
directory. Sweet spot ~100k vectors — comfortable for "all my notes",
"this codebase", "this PDF library" use cases.

- **Dep.** `github.com/philippgille/chromem-go` (MIT, active).
- **Persistence path.** Use `os.UserConfigDir()` →
  `Margo/vectors/<collection>.gob`. One collection per knowledge source.
- **Implementation.** Wrap `chromem.Collection` to satisfy
  `rag.VectorStore`. The chromem-go embedder slot accepts a
  `func(ctx, text) ([]float32, error)` — adapt our `Embedder`
  interface in.
- **Lifecycle.** Load collection on first query (lazy), persist
  asynchronously after writes. Don't block the chat thread on disk
  flushes.
- **Limits.** chromem-go loads the full index into RAM on open
  (HNSW + brute-force hybrid). Beyond ~100k vectors, switch the user
  to Qdrant. Surface this in the UI as a soft warning when a
  collection grows past, say, 50k entries.
- **Why pure Go matters here.** sqlite-vec would force CGo into
  universal macOS Wails builds and break the current
  zero-CGo-dependency property. chromem-go preserves it.

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

### 6.10 Tool middleware

`compose.ToolMiddleware` lets us wrap every tool invocation with
cross-cutting concerns: logging, rate-limiting, permission prompts
("the agent wants to call `delete_file(path=...)` — allow?"). The
permission-prompt use case is the most user-valuable; build it once
we have any tool that mutates state.

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
