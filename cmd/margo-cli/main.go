// Command margo-cli is a headless driver for the margo framework.
//
// It consumes pkg/margo/core.Session, the same orchestration root the
// desktop app and the TUI use, so every provider core knows about is
// reachable here without a per-binary switch statement to keep in sync.
// The previous version constructed provider clients directly and grew a
// hard-coded anthropic/openai switch, which left OpenRouter unreachable
// from the CLI even though config.Validate accepted its key.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"

	"github.com/shakfu/margo/internal/config"
	"github.com/shakfu/margo/pkg/margo/core"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "margo:", err)
		os.Exit(1)
	}
}

// run holds the body so failures return an error instead of calling
// log.Fatal from six places, and so the flow is testable.
func run() error {
	provider := flag.String("provider", "", "provider to use; defaults to the first one configured")
	model := flag.String("model", "", "model id; defaults to the provider's first catalog entry")
	prompt := flag.String("prompt", "", "prompt to send")
	system := flag.String("system", "", "optional system prompt")
	stream := flag.Bool("stream", false, "stream tokens to stdout as they arrive")
	list := flag.Bool("list", false, "list configured providers and their models, then exit")
	refresh := flag.Bool("refresh", false, "refresh the model catalog from the provider before running")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	sess := core.NewSession(core.Config{
		AnthropicAPIKey:  cfg.AnthropicAPIKey,
		OpenAIAPIKey:     cfg.OpenAIAPIKey,
		OpenRouterAPIKey: cfg.OpenRouterAPIKey,
	})
	defer sess.Shutdown()

	providers := sess.Providers()
	if len(providers) == 0 {
		return fmt.Errorf("no providers configured; set an API key in .env")
	}

	name := *provider
	if name == "" {
		name = providers[0]
	}
	if !slices.Contains(providers, name) {
		return fmt.Errorf("provider %q is not configured (have: %s)", name, strings.Join(providers, ", "))
	}

	ctx := context.Background()
	if *refresh {
		// -list prints every configured provider, so refresh every one;
		// refreshing only the selected provider would leave the rest
		// looking current when they are not. Without -list only the
		// provider about to be used is worth the round-trip.
		targets := []string{name}
		if *list {
			targets = providers
		}
		for _, p := range targets {
			if err := sess.RefreshModels(ctx, p); err != nil {
				// A stale catalog still works; say so and carry on.
				log.Printf("catalog refresh failed for %s, using cached models: %v", p, err)
			}
		}
	}

	if *list {
		for _, p := range providers {
			fmt.Printf("%s\n", p)
			for _, id := range sess.Models(p) {
				fmt.Printf("  %s\n", id)
			}
		}
		return nil
	}

	if strings.TrimSpace(*prompt) == "" {
		return fmt.Errorf("-prompt is required (or pass -list to see what is available)")
	}

	req := core.ChatRequest{
		Provider: name,
		System:   *system,
		Messages: []core.Message{{Role: "user", Content: *prompt}},
		Options:  core.Options{Model: *model},
	}

	if *stream {
		return runStream(ctx, sess, req)
	}
	resp, err := sess.Chat(ctx, req)
	if err != nil {
		return err
	}
	fmt.Println(resp.Text)
	return nil
}

func runStream(ctx context.Context, sess *core.Session, req core.ChatRequest) error {
	ch, err := sess.Stream(ctx, "cli", req)
	if err != nil {
		return err
	}
	for ev := range ch {
		switch ev.Kind {
		case core.EventText:
			fmt.Print(ev.Text)
		case core.EventError:
			fmt.Fprintln(os.Stderr)
			if ev.Err != nil {
				return ev.Err
			}
			return fmt.Errorf("%s", ev.Text)
		case core.EventDone:
			fmt.Println()
		}
	}
	return nil
}
