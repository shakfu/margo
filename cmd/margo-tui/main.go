// Command margo-tui is a Bubble Tea front-end for margo. It is a peer
// of the Wails desktop app (cmd/margo) — both consume the same
// pkg/margo/core.Session, neither imports the other. This binary is
// self-contained: it does not need the Svelte assets, the Wails runtime,
// or a display server.
//
// Current scope (scaffold): single-provider, single-model streaming chat
// against pkg/margo/core.Session.Stream. Agent runs, RAG indexing,
// attachments, and permission prompts are not wired yet — they are
// straightforward additions because the underlying core API already
// supports them; see TODOs inline.
package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/shakfu/margo/internal/config"
	"github.com/shakfu/margo/pkg/margo/core"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fatal("config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		fatal("config: %v", err)
	}

	sess := core.NewSession(core.Config{
		AnthropicAPIKey:  cfg.AnthropicAPIKey,
		OpenAIAPIKey:     cfg.OpenAIAPIKey,
		OpenRouterAPIKey: cfg.OpenRouterAPIKey,
	})

	providers := sess.Providers()
	if len(providers) == 0 {
		fatal("no providers configured: set ANTHROPIC_API_KEY, OPENAI_API_KEY, or OPENROUTER_API_KEY")
	}
	// Pick the first configured provider + its first model. Switching
	// providers/models is a follow-up — likely a Ctrl+P picker that
	// reads sess.Providers() / sess.Models(provider).
	provider := providers[0]
	models := sess.Models(provider)
	if len(models) == 0 {
		fatal("provider %q has no known models", provider)
	}
	modelID := models[0]

	p := tea.NewProgram(
		newModel(sess, provider, modelID),
		tea.WithAltScreen(),
	)
	if _, err := p.Run(); err != nil {
		fatal("tui: %v", err)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
