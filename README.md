# margo

A Go AI framework with a Wails (Svelte + TypeScript) desktop frontend.

Margo is two things in one repo:

1. **`pkg/margo`** — a small, importable, provider-agnostic client library for
   talking to LLM APIs. Currently supports Anthropic and OpenAI; both
   `Complete` and `Stream` (multi-turn).
2. **A desktop chat app** built on top of that library using Wails v2, with a
   three-pane LM Studio–inspired UI: collapsible chat-history sidebar,
   conversation view (markdown + syntax-highlighted code + LaTeX math), and
   collapsible model-parameters sidebar.

## Layout

```
margo/
├── main.go               # Wails app entrypoint (wails.Run)
├── app.go                # App struct; methods bound to the frontend
├── wails.json            # Wails project config
│
├── cmd/margo-cli/        # headless CLI driver for the framework
│
├── pkg/margo/            # importable framework
│   ├── client.go         # Client interface, Request/Response/Chunk types
│   └── providers/
│       ├── anthropic/
│       └── openai/
│
├── internal/config/      # godotenv-based env loading
│
├── frontend/
│   ├── src/
│   │   ├── App.svelte           # layout shell
│   │   └── lib/
│   │       ├── store.ts         # chats + settings (localStorage)
│   │       ├── ChatList.svelte
│   │       ├── SettingsPanel.svelte
│   │       ├── markdown.ts      # marked + hljs + DOMPurify; math-aware
│   │       └── mathjax.ts       # debounced typeset action
│   ├── public/
│   │   └── mathjax/             # vendored MathJax 3 SVG bundle
│   └── wailsjs/                 # auto-generated Go<->JS bindings
│
├── build/                # Wails packaging assets (icons, plists, etc.)
├── Makefile
├── CHANGELOG.md
├── TODO.md
└── .env.example
```

## Setup

Requirements: Go 1.23+, Node 20+, Wails v2 CLI (`go install
github.com/wailsapp/wails/v2/cmd/wails@latest`).

```sh
cp .env.example .env
# edit .env and set ANTHROPIC_API_KEY and/or OPENAI_API_KEY

make doctor             # check go / wails / node / npm versions
make frontend-install   # npm install + vendor mathjax bundle
make tidy               # go mod tidy
```

## Running

### Desktop app (Wails)

```sh
make dev      # live-reload dev server
make build    # production build → build/bin/margo.app
make run      # build then open the app
```

The app boots into an empty state. Type a message to auto-create your first
chat; conversations persist across reloads via `localStorage`. Toggle either
sidebar from the topbar arrows. Theme defaults to light; toggle in the right
panel under "Appearance".

### Headless CLI

```sh
make cli-run ARGS="-provider anthropic -prompt 'What is a quaternion?'"
make cli-run ARGS="-provider openai -prompt 'haiku about computers' -stream"
```

Flags: `-provider {anthropic|openai}`, `-prompt <text>`, `-system <text>`,
`-stream`.

## Features

- **Multi-provider:** Anthropic and OpenAI today; add more by implementing
  `margo.Client` in `pkg/margo/providers/`.
- **Streaming:** token-by-token delivery into the UI, cancellable mid-stream.
- **Markdown + code:** GFM via `marked`; syntax highlighting for ~30 common
  languages via `highlight.js`; sanitized with `dompurify`.
- **Math:** vendored MathJax 3 (SVG output, no external font loads, no CDN).
  Inline `$x$` / `\(x\)` and display `$$x$$` / `\[x\]` both work, including
  matrices and `\\` line breaks.
- **Multiple chats:** persisted to `localStorage`; rename, two-click delete,
  search.
- **Light + dark theme:** persisted; CSS variables drive both; `highlight.js`
  themes swapped to match.
- **No CDN dependencies:** every JS/CSS asset is bundled or vendored. The
  app works fully offline once provider API endpoints are reachable.

## Useful Make targets

`make help` lists everything. Highlights:

| Target | What it does |
|---|---|
| `make dev` | `wails dev` (live reload) |
| `make build` | Production Wails build → `build/bin/` |
| `make build-universal` | macOS arm64+amd64 universal build |
| `make cli` | Build the headless CLI binary |
| `make vendor-mathjax` | Refresh the vendored MathJax bundle from `node_modules` |
| `make bindings` | Regenerate `frontend/wailsjs/go/` bindings |
| `make test` / `make cover` | Go tests / coverage |
| `make doctor` | Check toolchain versions |
| `make clean-all` | Remove all build artifacts and `node_modules` |

## Build outputs

Everything lands under `build/bin/`:

- `build/bin/margo.app` (macOS) / `margo` (Linux) / `margo.exe` (Windows) —
  desktop app
- `build/bin/margo-cli` — headless CLI

`frontend/dist/` is the Vite build output that Wails embeds into the Go binary
via `//go:embed all:frontend/dist`. Both `build/bin/`, `frontend/dist/`, and
`frontend/node_modules/` are gitignored. The vendored MathJax bundle
(`frontend/public/mathjax/`) is committed.

## Contributing

See `CHANGELOG.md` for what has shipped and `TODO.md` for what is next on the
list.

## License

MIT — see `LICENSE`.
