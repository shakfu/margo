# margo

A Go AI framework with three interchangeable front-ends: a Wails desktop app, a terminal UI, and a headless CLI.

Margo is two things in one repo:

1. **`pkg/margo`** — an importable, provider-agnostic client library for LLM APIs. Anthropic, OpenAI, and OpenRouter today; `Complete` and `Stream`, multi-turn, with tool calling and multimodal input.

2. **A desktop chat app** built on that library with Wails v2 and Svelte: a three-pane LM Studio-inspired UI with chat history, a conversation view (markdown, syntax-highlighted code, LaTeX math), and a model-parameters sidebar.

Between the two sits **`pkg/margo/core`**, the UI-agnostic orchestration layer. It returns Go channels of events, so the desktop app, the TUI, and the CLI all consume the same code. None of them is privileged.

## Layout

```
margo/
├── main.go               # Wails app entrypoint (wails.Run)
├── app.go                # App struct; methods bound to the frontend
├── wails.json            # Wails project config
│
├── cmd/
│   ├── margo-cli/        # headless CLI driver
│   └── margo-tui/        # Bubble Tea terminal UI
│
├── pkg/margo/            # importable framework
│   ├── client.go         # Client + ModelLister interfaces; Request/Response/Chunk
│   ├── models.go         # Catalog types
│   ├── models.json       # embedded model seed + metadata overlay
│   ├── catalog_cache.go  # live catalog fetch, disk cache, merge
│   ├── docs.go           # PDF/document text extraction
│   ├── core/             # UI-agnostic orchestration (Session, events)
│   ├── agent/            # runners, tools, permissions, context budget
│   ├── mcp/              # Model Context Protocol client + subprocess manager
│   ├── rag/              # embedder, chunker, vector store, indexer
│   └── providers/
│       ├── openaicompat/ # shared OpenAI Chat Completions wire format
│       ├── anthropic/
│       ├── openai/
│       └── openrouter/
│
├── internal/
│   ├── config/           # godotenv-based env loading
│   └── pathsafe/         # path containment for untrusted inputs
│
├── frontend/
│   ├── src/
│   │   ├── App.svelte           # layout shell
│   │   └── lib/
│   │       ├── store/           # chats + settings (localStorage)
│   │       ├── stream.ts        # stream-event routing
│   │       ├── slash.ts         # slash-command parser
│   │       ├── markdown.ts      # marked + hljs + DOMPurify; math-aware
│   │       ├── mathjax.ts       # debounced typeset action
│   │       └── settings/        # settings panel subcomponents
│   ├── public/mathjax/          # vendored MathJax 3 SVG bundle
│   └── wailsjs/                 # auto-generated Go<->JS bindings
│
├── docs/                 # architecture.md, concepts.md, dev/
├── build/                # Wails packaging assets
└── .github/workflows/    # CI
```

`docs/architecture.md` is the contributor-facing tour; `docs/concepts.md` explains the persona / agent / tool / workspace taxonomy.

## Setup

Requirements: Go 1.24+, Node 20+, Wails v2 CLI (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`).

Wails v2.11.0 pins `golang.org/x/tools` v0.30.0, which cannot read the export data Go 1.27 writes; every wails subcommand that analyses the module fails with `internal error: package "errors" without types was imported from ...`. The Makefile therefore pins `GOTOOLCHAIN=go1.26.2` for wails targets only — `go build`, `go test`, and `go vet` use whatever Go is installed. Override with `make dev WAILS_GOTOOLCHAIN=go1.27.0` once wails ships a newer x/tools. Note that a `toolchain` directive in `go.mod` does not solve this: it sets a minimum, so a newer local Go still wins.

```sh
cp .env.example .env
# edit .env: set at least one of ANTHROPIC_API_KEY, OPENAI_API_KEY, OPENROUTER_API_KEY

make doctor             # check go / wails / node / npm versions
make frontend-install   # npm install + vendor mathjax bundle
make tidy               # go mod tidy
```

`OPENAI_API_KEY` doubles as the embedder key for knowledge indexing. Without it the Knowledge tab indexes nothing.

## Running

### Desktop app (Wails)

```sh
make dev      # live-reload dev server
make build    # production build -> build/bin/margo.app
make run      # build then open the app
```

The app boots into an empty state. Type a message to auto-create your first chat; conversations persist across reloads via `localStorage`. Toggle either sidebar from the topbar arrows. Settings open with Cmd+, or from the right panel.

### Terminal UI

```sh
make tui-run
```

Streaming chat against the first configured provider. Enter sends, Ctrl+C cancels an in-flight stream or quits when idle.

### Headless CLI

```sh
make cli-run ARGS="-list"
make cli-run ARGS="-prompt 'What is a quaternion?'"
make cli-run ARGS="-provider openrouter -model deepseek/deepseek-v3.2 -prompt 'haiku about computers' -stream"
```

Flags: `-provider`, `-model`, `-prompt`, `-system`, `-stream`, `-list`, `-refresh`. Provider and model default to the first configured entries. `-refresh` re-fetches the selected provider's catalog, or every configured provider when combined with `-list`.

## Features

- **Multi-provider.** Anthropic, OpenAI, and OpenRouter. Add more by implementing `margo.Client` under `pkg/margo/providers/`; anything speaking the OpenAI Chat Completions format can wrap `openaicompat` instead of reimplementing it.

- **Live model catalog.** Model lists are fetched from each provider, cached under `<UserConfigDir>/Margo/catalog/` with a 24h TTL, and merged over the embedded `models.json` for metadata the provider does not report. Falls back to the cache, then to the embedded seed, so the app works offline. The last model used is remembered per provider.

- **Streaming.** Token-by-token delivery into the UI, cancellable mid-stream.

- **Agents.** Three runners behind slash commands: `/agent` (ReAct), `/agent-plan` (plan-then-execute), `/agent-workflow` (drafter → critic → refiner). Built on Eino's ADK.

- **Tools.** `current_time`, `web_fetch`, `search_knowledge`, and `quarto_render` when the `quarto` binary is on PATH. Each non-read-only call is gated by a permission prompt.

- **MCP.** Model Context Protocol client over stdio, Claude-Desktop-compatible config at `<UserConfigDir>/Margo/mcp.json`. Servers start eagerly and asynchronously; tools namespace as `mcp:<server>:<tool>`.

- **Knowledge / RAG.** Index files or folders into a per-workspace vector store (chromem-go, in-process). The agent retrieves through `search_knowledge` and the UI renders citation cards.

- **Workspaces.** Per-workspace chats, tool palettes, settings overrides, personas, and knowledge index. Note the two scopes: in a named workspace, right-pane settings changes persist with that workspace; in the **Default** workspace they are session-scoped and reset on restart, since Default is a scratch layer over your global defaults. Cmd+, edits the durable global values. The model is the exception — it is remembered per provider and re-applied on launch regardless of scope.

- **Personas.** A named voice (system prompt) per chat, from a builtin catalog plus your own.

- **Attachments.** Images and PDFs, drag-drop or paste. Anthropic takes PDFs natively; other providers get server-side text extraction. Attachments from earlier turns are re-fed on follow-up questions.

- **Markdown, code, math.** GFM via `marked`, ~30 languages via `highlight.js`, sanitized with `dompurify`. Vendored MathJax 3 (SVG output, no CDN): inline `$x$` / `\(x\)` and display `$$x$$` / `\[x\]`, matrices included.

- **Cost meter and context gauge.** Running USD estimate and context-window usage per chat. Each turn is priced against the model that produced it, so switching model mid-chat does not reprice the history. The header shows the total; click it for the per-model split. A `+` suffix means some turns have no declared rate and the total is a floor.

- **Markdown export.** Serialise a chat to a `.md` file, including attachments and tool steps.

- **Light + dark theme**, persisted, with `highlight.js` themes swapped to match.

- **No CDN dependencies.** Every JS/CSS asset is bundled or vendored. The app works offline once provider endpoints are reachable.

## Security posture

margo runs model-authored instructions against your machine, so a few things are deliberately restrictive:

- **Tool calls are gated.** Anything that is not read-only prompts before running. "Always approve" is offered per tool and persisted, except for tools whose risk lives in their arguments (`quarto_render`) — those are approved one call at a time.
- **`quarto_render` is sandboxed.** Both the document it writes and its `--output-dir` are confined to `~/Documents/Margo/outputs/`. Computational cells are not executed unless `core.Config.QuartoExecute` is set.
- **`web_fetch` refuses non-public addresses.** Loopback, LAN, link-local (including cloud metadata endpoints), and CGNAT are blocked at dial time, on the initial request and on every redirect. Lift with `core.Config.AllowPrivateNetwork`.
- **`OpenPath` is confined** to margo's output and attachment directories, so a model-authored `file://` link cannot open arbitrary paths.
- **API keys never reach the frontend.** They load from the environment and stay in the Go process.

## Useful Make targets

`make help` lists everything. Highlights:

| Target | What it does |
|---|---|
| `make dev` | `wails dev` (live reload) |
| `make build` | Production Wails build -> `build/bin/` |
| `make build-universal` | macOS arm64+amd64 universal build |
| `make cli` / `make tui` | Build the CLI / TUI binaries |
| `make test` | Go tests |
| `make test-frontend` | Vitest frontend tests |
| `make test-all` | Both |
| `make test-integration` | MCP integration tests (needs `npx`) |
| `make cover` | Go coverage summary |
| `make fmt` / `make vet` / `make lint` | Formatting and static analysis |
| `make vendor-mathjax` | Refresh the vendored MathJax bundle |
| `make bindings` | Regenerate `frontend/wailsjs/go/` bindings |
| `make doctor` | Check toolchain versions |
| `make clean-all` | Remove all build artifacts and `node_modules` |

CI runs `gofmt -l`, `go vet`, `go test -race`, `svelte-check`, and `vitest` on every push and pull request.

## Build outputs

Everything lands under `build/bin/`:

- `build/bin/margo.app` (macOS) / `margo` (Linux) / `margo.exe` (Windows) — desktop app
- `build/bin/margo-cli` — headless CLI
- `build/bin/margo-tui` — terminal UI

`frontend/dist/` is the Vite build output that Wails embeds via `//go:embed all:frontend/dist`. `build/bin/`, `frontend/dist/`, and `frontend/node_modules/` are gitignored. The vendored MathJax bundle (`frontend/public/mathjax/`) is committed.

## Contributing

See `CHANGELOG.md` for what has shipped and `TODO.md` for what is next.

## License

MIT — see `LICENSE`.
