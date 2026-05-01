# margo

A Go AI framework with a planned Wails desktop frontend.

## Layout

```
margo/
├── cmd/margo-cli/      # headless CLI for testing the framework
├── pkg/margo/          # provider-agnostic Client interface (importable)
│   └── providers/
│       ├── anthropic/
│       └── openai/
├── internal/config/    # godotenv-based env loading
├── .env.example
└── go.mod
```

The Wails desktop app (root `main.go`, `app.go`, `wails.json`, `frontend/`, `build/`) will be added by running `wails init` at the repo root and importing `github.com/shakfu/margo/pkg/margo`.

## Setup

```sh
cp .env.example .env
# edit .env and set ANTHROPIC_API_KEY and/or OPENAI_API_KEY
go mod tidy
```

## CLI usage

```sh
go run ./cmd/margo-cli -provider anthropic -prompt "What is a quaternion?"
go run ./cmd/margo-cli -provider openai    -prompt "Write me a haiku about computers"
```
