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

### 6.3 Context-window management via MessageRewriter — **done**

Drop-oldest variant shipped. `pkg/margo/agent/budget.go` provides
`BudgetForModel(model)` (Go-side mirror of the frontend's
`CONTEXT_WINDOWS` table), `RewriteForBudget(msgs, budget)` (operates
on `[]*schema.Message` for the agent path), and
`RewriteMargoForBudget(msgs, system, budget)` (operates on
`[]margo.Message` for the plain-chat path). Both registered: the
agent path via `react.AgentConfig.MessageRewriter` (so trimming runs
between ReAct iterations as tool results accumulate); the plain-chat
path via `toMargoRequest` in `app.go`. Algorithm trims oldest turns
under a 25% output reserve, keeps system + final turn always, and
groups Tool messages with their owning Assistant turn so tool_results
are never orphaned. Token estimation is `len/4` chars-per-token —
deliberately coarse, no tokenizer dep. Coverage:
`budget_test.go::TestRewriteForBudget*` + `TestRewriteMargoForBudget`.
Docs: `docs/dev/agents_and_tools.md` § "Context-window management".

Deferred from the original task: summarisation-instead-of-drop (would
preserve information but adds a model call per iteration); injecting
ephemeral system reminders (waits on a per-chat preferences store
that doesn't exist yet).

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

### 6.10 Tool middleware — **partially done** (permission prompts shipped)

Permission-prompt slice landed:
`pkg/margo/agent/permission.go::permissionMiddleware` gates each
non-read-only invocation behind a user prompt; read-only tools
auto-approve via the `agent.ReadOnlyTools` allowlist. UI surfaces
prompts as a step card with Approve / Always / Deny buttons; the
Always list persists in `Settings.autoApproveTools`. Coverage:
`permission_test.go`. Docs: `docs/dev/agents_and_tools.md` § "Tool
permission prompts".

Remaining cross-cutting middleware uses (not yet built; pursue when
demand surfaces):

- **Logging / tracing**: a middleware that logs invocation name +
  args + duration to a sink. Useful for debugging agent runs in
  production.
- **Rate limiting**: per-tool or per-key rate limits. Relevant once
  we have network-bound tools.
- ~~**Trusted-tools management UI**: a Settings panel section showing
  the persisted `autoApproveTools` list with per-entry remove
  buttons.~~ Done — collapsible "Trusted tools" section in
  `SettingsPanel.svelte` with per-entry Revoke buttons + a Revoke-all
  shortcut.

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

### 7.1 Provider multipart message shape (foundation)

Extend `margo.Message` to carry structured content parts in addition to
its current `Content string`. Concrete:

- New `Part` type: `{Kind: "text"|"image"|"document", Text string,
  MimeType string, Data []byte}` (bytes inlined for now; revisit if
  large files become common).
- `Message.Parts []Part` field. `Content` stays as the legacy
  text-only convenience; serializer prefers `Parts` when non-empty.
- Per-provider conversion in `pkg/margo/providers/{anthropic,openai,
  openrouter}`:
  - **Anthropic**: each Part maps to `messages.ContentBlockParamUnion`
    via `image` (base64) or `document` (PDF) blocks. The SDK already
    expects multipart on user turns, so the conversion is mechanical.
  - **OpenAI / OpenRouter**: parts map to `ChatCompletionContentPart`
    with `image_url` carrying a `data:<mime>;base64,...` URL.
- No UI yet — this slice is the wire-shape foundation. Cover with
  unit tests that round-trip a `Message{Parts: [text+image]}` through
  each provider's `to<SDK>Messages` converter.

### 7.2 Image attachments end-to-end (Anthropic-only first slice)

Smallest user-visible slice. Pick the most-tested multimodal model
(Anthropic Claude Sonnet/Opus 4.x) and ship images-only.

- **Wails surface.** New `App.AttachFile() ([]AttachedFile, error)`
  that calls `runtime.OpenFileDialog` (or accepts a drag-drop
  payload — see below) and returns base64-encoded bytes + mime type
  + filename. Cap accepted MIMEs to `image/png`, `image/jpeg`,
  `image/webp`, `image/gif`. Cap per-file size at e.g. 10MB.
- **Composer UI.** Paperclip button that opens the file dialog.
  Drag-drop zone over the composer. Selected images render as small
  thumbnails above the textarea with a `×` to remove. Bound to a
  Svelte store keyed by the active chat id so the user can attach
  multiple images before sending.
- **Send path.** `StreamChat` / `StreamAgent` get an additional
  `attachments []AttachedFile` arg; the bindings translate them into
  `margo.Message.Parts` on the latest user turn. Past turns remain
  text-only for now (see #7.4).
- **Model gating.** When attachments are present, force the model
  selector to the multimodal allowlist (Anthropic only this slice).
  Subsequent sends after the attachment is consumed re-allow all
  models.
- Coverage: a fake-client integration test asserting that an image
  attachment shows up as an `image` block in the request the
  Anthropic provider would send.

### 7.3 Cross-provider parity

Extend #7.2's image path to OpenAI and OpenRouter using each SDK's
multimodal content shape. The provider-converter work landed in #7.1
already; this slice expands the multimodal allowlist in
`frontend/src/lib/store.ts` and tests the OpenAI-side wire conversion.
GPT-4o family + OpenRouter's vision-capable models become eligible.

### 7.4 Persistence + replay

Today's `localStorage`-backed `Chat.messages` only stores text. After
#7.2/#7.3, attached images are sent once and forgotten. Decide:

- **Option A: store base64 in localStorage.** Simple, but localStorage
  has a ~5MB origin quota and images blow through it fast.
- **Option B: write attachments to `~/Documents/Margo/attachments/`
  (mirroring `outputs/`) and store only the path in `Chat.messages`.**
  Re-sending the chat reads bytes from disk. This is the right
  long-term answer; pairs with the existing `OpenPath` plumbing for
  click-to-open of attached images in the chat history.
- Schema migration for the existing `margo:chats:v1` localStorage
  shape: add `parts?: Part[]` to message; existing messages with
  only `content` keep working unchanged.

Defer the choice until #7.2 is shipped and we have feel for typical
attachment sizes.

### 7.5 Document (PDF / text) attachments

Once images work, extend to documents. Two paths converge:

- **Anthropic native**: PDFs ride as `document` blocks (the SDK
  already supports this). No preprocessing.
- **OpenAI / OpenRouter fallback**: extract text on the Go side
  (`github.com/ledongthuc/pdf` for PDFs, `os.ReadFile` for plain
  text/markdown) and inline as a text part with a `<file
  name="...">` wrapper. Loses structure but works on any model.

Per-file size cap higher than images (~25MB). Token cost for
extracted text is real — feeds into #7.6.

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
