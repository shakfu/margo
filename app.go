package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

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

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func NewApp() *App {
	cfg, _ := config.Load()
	a := &App{cfg: cfg, cancels: map[string]context.CancelFunc{}}
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

// Greet is the stock Wails template method, retained for reference.
func (a *App) Greet(name string) string {
	return fmt.Sprintf("Hello %s, It's show time!", name)
}

func (a *App) clientFor(provider string) (margo.Client, error) {
	var c margo.Client
	switch provider {
	case "anthropic":
		c = a.anthropic
	case "openai":
		c = a.openai
	default:
		return nil, fmt.Errorf("unknown provider: %s", provider)
	}
	if c == nil {
		return nil, fmt.Errorf("provider %q not configured (missing API key)", provider)
	}
	return c, nil
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

// ChatMessage mirrors margo.Message for JSON binding to the frontend.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func toMargoMessages(in []ChatMessage) []margo.Message {
	out := make([]margo.Message, len(in))
	for i, m := range in {
		role := margo.RoleUser
		if m.Role == "assistant" {
			role = margo.RoleAssistant
		}
		out[i] = margo.Message{Role: role, Content: m.Content}
	}
	return out
}

// Chat performs a non-streaming multi-turn completion.
func (a *App) Chat(provider, system string, messages []ChatMessage) (string, error) {
	c, err := a.clientFor(provider)
	if err != nil {
		return "", err
	}
	resp, err := c.Complete(a.ctx, margo.Request{
		System:   system,
		Messages: toMargoMessages(messages),
	})
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}

// StreamChat starts a streaming completion. The caller (frontend) provides the
// stream id, which lets it subscribe to events *before* this call so no chunks
// are dropped. Events emitted:
//
//	margo:stream:<id>:chunk  payload = string (token delta)
//	margo:stream:<id>:error  payload = string (error message)
//	margo:stream:<id>:done   payload = nil
//
// Cancel an in-flight stream with CancelStream(id).
func (a *App) StreamChat(id, provider, system string, messages []ChatMessage) error {
	c, err := a.clientFor(provider)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(a.ctx)
	a.mu.Lock()
	if _, exists := a.cancels[id]; exists {
		a.mu.Unlock()
		cancel()
		return fmt.Errorf("stream id %q already in use", id)
	}
	a.cancels[id] = cancel
	a.mu.Unlock()

	ch, err := c.Stream(ctx, margo.Request{
		System:   system,
		Messages: toMargoMessages(messages),
	})
	if err != nil {
		a.mu.Lock()
		delete(a.cancels, id)
		a.mu.Unlock()
		cancel()
		return err
	}

	go func() {
		base := "margo:stream:" + id
		defer func() {
			a.mu.Lock()
			delete(a.cancels, id)
			a.mu.Unlock()
			cancel()
		}()
		for chunk := range ch {
			if chunk.Err != nil {
				runtime.EventsEmit(a.ctx, base+":error", chunk.Err.Error())
				return
			}
			runtime.EventsEmit(a.ctx, base+":chunk", chunk.Text)
		}
		runtime.EventsEmit(a.ctx, base+":done")
	}()
	return nil
}

// CancelStream cancels an in-flight stream. No-op if the id is unknown.
func (a *App) CancelStream(id string) {
	a.mu.Lock()
	cancel := a.cancels[id]
	delete(a.cancels, id)
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
