package main

import (
	"context"
	"fmt"

	"github.com/shakfu/margo/internal/config"
	"github.com/shakfu/margo/pkg/margo"
	"github.com/shakfu/margo/pkg/margo/providers/anthropic"
	"github.com/shakfu/margo/pkg/margo/providers/openai"
)

// App is the Wails-bound struct. Exported methods are callable from the frontend
// via the auto-generated bindings in frontend/wailsjs/go/main/App.{js,d.ts}.
type App struct {
	ctx       context.Context
	cfg       *config.Config
	anthropic margo.Client
	openai    margo.Client
}

func NewApp() *App {
	cfg, _ := config.Load()
	a := &App{cfg: cfg}
	if cfg.AnthropicAPIKey != "" {
		a.anthropic = anthropic.New(cfg.AnthropicAPIKey)
	}
	if cfg.OpenAIAPIKey != "" {
		a.openai = openai.New(cfg.OpenAIAPIKey)
	}
	return a
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// Greet is the stock Wails template method, retained so the default frontend
// keeps working until you replace App.svelte with a real chat UI.
func (a *App) Greet(name string) string {
	return fmt.Sprintf("Hello %s, It's show time!", name)
}

// Providers returns the list of providers that have an API key configured.
func (a *App) Providers() []string {
	out := []string{}
	if a.anthropic != nil {
		out = append(out, a.anthropic.Name())
	}
	if a.openai != nil {
		out = append(out, a.openai.Name())
	}
	return out
}

// Ask sends a single-turn prompt to the named provider and returns the response text.
func (a *App) Ask(provider, prompt string) (string, error) {
	var c margo.Client
	switch provider {
	case "anthropic":
		c = a.anthropic
	case "openai":
		c = a.openai
	default:
		return "", fmt.Errorf("unknown provider: %s", provider)
	}
	if c == nil {
		return "", fmt.Errorf("provider %q not configured (missing API key)", provider)
	}
	resp, err := c.Complete(a.ctx, margo.Request{Prompt: prompt})
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}
