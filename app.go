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
	"github.com/shakfu/margo/pkg/margo/providers/openrouter"
)

// App is the Wails-bound struct. Exported methods are callable from the frontend
// via the auto-generated bindings in frontend/wailsjs/go/main/App.{js,d.ts}.
type App struct {
	ctx        context.Context
	cfg        *config.Config
	anthropic  margo.Client
	openai     margo.Client
	openrouter margo.Client

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
	if cfg.OpenRouterAPIKey != "" {
		a.openrouter = openrouter.New(cfg.OpenRouterAPIKey)
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
	case "openrouter":
		c = a.openrouter
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
	if a.openrouter != nil {
		out = append(out, a.openrouter.Name())
	}
	return out
}

// Models returns the list of model identifiers we expose for a provider. The
// frontend uses this to populate the Model picker. The first entry is the
// default.
func (a *App) Models(provider string) []string {
	switch provider {
	case "anthropic":
		return []string{
			"claude-haiku-4-5",
			"claude-sonnet-4-6",
			"claude-opus-4-7",
		}
	case "openai":
		return []string{
			"gpt-5.4-nano",
			"gpt-5.4-mini",
			"gpt-5.4",
			"gpt-5.4-pro",
			"gpt-5.5",
			"gpt-5.5-pro",
		}
	case "openrouter":
		return []string{
			"deepseek/deepseek-v3.2",
			"deepseek/deepseek-v4-flash",
			"deepseek/deepseek-v4-pro",
			"google/gemini-2.5-flash",
			"google/gemini-2.5-flash-lite",
			"google/gemini-3-flash-preview",
			"google/gemma-4-26b-a4b-it:free",
			"google/gemma-4-31b-it:free",
			"moonshotai/kimi-k2.5",
			"moonshotai/kimi-k2.6",
			"nvidia/nemotron-3-super-120b-a12b:free",
			"openrouter/owl-alpha",
			"qwen/qwen3-235b-a22b-2507",
			"qwen/qwen3.5-flash-02-23",
			"qwen/qwen3.6-plus",
			"x-ai/grok-4.1-fast",
			"x-ai/grok-4.3",
		}
	}
	return []string{}
}

// ChatMessage mirrors margo.Message for JSON binding to the frontend.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatOptions carries per-request sampling and reasoning settings from the
// frontend. Pointer fields (Temperature, TopP) are omitted when nil so the
// provider falls back to its default.
type ChatOptions struct {
	Model         string   `json:"model"`
	MaxTokens     int      `json:"maxTokens"`
	Temperature   *float64 `json:"temperature"`
	TopP          *float64 `json:"topP"`
	StopSequences []string `json:"stopSequences"`
	ThinkEnabled  bool     `json:"thinkEnabled"`
	ThinkBudget   int      `json:"thinkBudget"`
}

// StreamChunkEvent is the payload for the `margo:stream:<id>:chunk` event.
type StreamChunkEvent struct {
	Kind string `json:"kind"` // "text" | "thinking"
	Text string `json:"text"`
}

// StreamUsage is the timing/token report emitted alongside :done.
type StreamUsage struct {
	InputTokens  int   `json:"inputTokens"`
	OutputTokens int   `json:"outputTokens"`
	FirstTokenMs int64 `json:"firstTokenMs"`
	TotalMs      int64 `json:"totalMs"`
}

// StreamDoneEvent is the payload for the `margo:stream:<id>:done` event.
type StreamDoneEvent struct {
	Usage *StreamUsage `json:"usage"`
}

// ChatResponse is the non-streaming completion result returned to the frontend.
type ChatResponse struct {
	Text     string      `json:"text"`
	Thinking string      `json:"thinking"`
	Model    string      `json:"model"`
	Usage    StreamUsage `json:"usage"`
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

func toMargoRequest(system string, messages []ChatMessage, opts ChatOptions) margo.Request {
	req := margo.Request{
		Model:         opts.Model,
		System:        system,
		Messages:      toMargoMessages(messages),
		MaxTokens:     opts.MaxTokens,
		Temperature:   opts.Temperature,
		TopP:          opts.TopP,
		StopSequences: opts.StopSequences,
	}
	if opts.ThinkEnabled {
		req.Thinking = &margo.Thinking{Enabled: true, BudgetTokens: opts.ThinkBudget}
	}
	return req
}

// Chat performs a non-streaming multi-turn completion.
func (a *App) Chat(provider, system string, messages []ChatMessage, opts ChatOptions) (ChatResponse, error) {
	c, err := a.clientFor(provider)
	if err != nil {
		return ChatResponse{}, err
	}
	resp, err := c.Complete(a.ctx, toMargoRequest(system, messages, opts))
	if err != nil {
		return ChatResponse{}, err
	}
	return ChatResponse{
		Text:     resp.Text,
		Thinking: resp.Thinking,
		Model:    resp.Model,
		Usage: StreamUsage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
		},
	}, nil
}

// StreamChat starts a streaming completion. The caller (frontend) provides the
// stream id, which lets it subscribe to events *before* this call so no chunks
// are dropped. Events emitted:
//
//	margo:stream:<id>:chunk  payload = StreamChunkEvent {kind, text}
//	margo:stream:<id>:error  payload = string (error message)
//	margo:stream:<id>:done   payload = StreamDoneEvent {usage}
//
// Cancel an in-flight stream with CancelStream(id).
func (a *App) StreamChat(id, provider, system string, messages []ChatMessage, opts ChatOptions) error {
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

	ch, err := c.Stream(ctx, toMargoRequest(system, messages, opts))
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
		var lastUsage *margo.Usage
		for chunk := range ch {
			if chunk.Err != nil {
				runtime.EventsEmit(a.ctx, base+":error", chunk.Err.Error())
				return
			}
			if chunk.Usage != nil {
				lastUsage = chunk.Usage
				continue
			}
			kind := string(chunk.Kind)
			if kind == "" {
				kind = string(margo.ChunkText)
			}
			runtime.EventsEmit(a.ctx, base+":chunk", StreamChunkEvent{Kind: kind, Text: chunk.Text})
		}
		var done StreamDoneEvent
		if lastUsage != nil {
			done.Usage = &StreamUsage{
				InputTokens:  lastUsage.InputTokens,
				OutputTokens: lastUsage.OutputTokens,
				FirstTokenMs: lastUsage.FirstTokenMs,
				TotalMs:      lastUsage.TotalMs,
			}
		}
		runtime.EventsEmit(a.ctx, base+":done", done)
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
