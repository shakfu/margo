package core

import (
	"context"
	"fmt"
	"sync"

	"github.com/shakfu/margo/pkg/margo"
	"github.com/shakfu/margo/pkg/margo/agent"
	"github.com/shakfu/margo/pkg/margo/providers/anthropic"
	"github.com/shakfu/margo/pkg/margo/providers/openai"
	"github.com/shakfu/margo/pkg/margo/providers/openrouter"
)

// Config carries the inputs needed to construct a Session. AttachmentRoot
// is normally "" (= use os.UserConfigDir); tests override it.
type Config struct {
	AnthropicAPIKey  string
	OpenAIAPIKey     string
	OpenRouterAPIKey string
	AttachmentRoot   string
}

// Session is margo's UI-agnostic orchestration root: it owns the
// configured provider clients, the workspace registry, the attachment
// store, the permission broker, and the per-stream cancel registry.
//
// Streaming methods return <-chan Event. Each frontend consumes the
// channel and translates events to its own transport (Wails event emit,
// Bubble Tea Msg, SSE, …).
type Session struct {
	anthropic  margo.Client
	openai     margo.Client
	openrouter margo.Client

	workspaces  *WorkspaceRegistry
	attachments *AttachmentStore
	permissions *PermissionBroker

	cancelsMu sync.Mutex
	cancels   map[string]runHandle
}

// runHandle bundles the per-stream cancel function with the derived
// context so workers can read both off the registry.
type runHandle struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// NewSession builds a Session from the given Config. Unconfigured
// providers (missing API key) simply do not appear in Providers().
func NewSession(cfg Config) *Session {
	s := &Session{
		workspaces:  NewWorkspaceRegistry(cfg.OpenAIAPIKey),
		attachments: NewAttachmentStore(cfg.AttachmentRoot),
		permissions: NewPermissionBroker(),
		cancels:     map[string]runHandle{},
	}
	if cfg.AnthropicAPIKey != "" {
		s.anthropic = anthropic.New(cfg.AnthropicAPIKey)
	}
	if cfg.OpenAIAPIKey != "" {
		s.openai = openai.New(cfg.OpenAIAPIKey)
	}
	if cfg.OpenRouterAPIKey != "" {
		s.openrouter = openrouter.New(cfg.OpenRouterAPIKey)
	}
	return s
}

// Workspaces exposes the registry for IndexPath / Sources / SetActive
// calls from the frontend.
func (s *Session) Workspaces() *WorkspaceRegistry { return s.workspaces }

// Attachments exposes the attachment store.
func (s *Session) Attachments() *AttachmentStore { return s.attachments }

// Permissions exposes the permission broker so the frontend can call
// Respond when the user clicks Allow/Deny on a prompt.
func (s *Session) Permissions() *PermissionBroker { return s.permissions }

// clientFor resolves a provider name to a configured client.
func (s *Session) clientFor(provider string) (margo.Client, error) {
	var c margo.Client
	switch provider {
	case "anthropic":
		c = s.anthropic
	case "openai":
		c = s.openai
	case "openrouter":
		c = s.openrouter
	default:
		return nil, fmt.Errorf("unknown provider: %s", provider)
	}
	if c == nil {
		return nil, fmt.Errorf("provider %q not configured (missing API key)", provider)
	}
	return c, nil
}

// Providers returns the names of every provider with an API key configured.
func (s *Session) Providers() []string {
	out := []string{}
	if s.anthropic != nil {
		out = append(out, s.anthropic.Name())
	}
	if s.openai != nil {
		out = append(out, s.openai.Name())
	}
	if s.openrouter != nil {
		out = append(out, s.openrouter.Name())
	}
	return out
}

// Models returns the model identifiers exposed for a provider. First entry
// is the default. Lists are baked in here because no provider's catalog
// API gives us a clean curated subset that matches the ones we test
// against; revisit when we add per-user model preferences.
func (s *Session) Models(provider string) []string {
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

// Chat is a non-streaming multi-turn completion.
func (s *Session) Chat(ctx context.Context, req ChatRequest) (Response, error) {
	c, err := s.clientFor(req.Provider)
	if err != nil {
		return Response{}, err
	}
	resp, err := c.Complete(ctx, toMargoRequest(req.System, req.Messages, req.Options, req.Attachments))
	if err != nil {
		return Response{}, err
	}
	return Response{
		Text:     resp.Text,
		Thinking: resp.Thinking,
		Model:    resp.Model,
		Usage: Usage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
		},
	}, nil
}

// Stream starts a streaming completion. The caller-supplied id keys the
// cancel registry; pass it back to Cancel(id) to abort. The returned
// channel is closed when the run ends; on error the final Event carries
// EventError + Err.
func (s *Session) Stream(ctx context.Context, id string, req ChatRequest) (<-chan Event, error) {
	c, err := s.clientFor(req.Provider)
	if err != nil {
		return nil, err
	}
	if err := s.registerCancel(ctx, id); err != nil {
		return nil, err
	}
	runCtx, _ := s.ctxFor(id)

	in, err := c.Stream(runCtx, toMargoRequest(req.System, req.Messages, req.Options, req.Attachments))
	if err != nil {
		s.releaseCancel(id)
		return nil, err
	}

	out := make(chan Event, 16)
	go func() {
		defer close(out)
		defer s.releaseCancel(id)

		var lastUsage *margo.Usage
		for chunk := range in {
			if chunk.Err != nil {
				out <- Event{Kind: EventError, Err: chunk.Err, Text: chunk.Err.Error()}
				return
			}
			if chunk.Usage != nil {
				lastUsage = chunk.Usage
				continue
			}
			kind := EventText
			if chunk.Kind == margo.ChunkThinking {
				kind = EventThinking
			}
			out <- Event{Kind: kind, Text: chunk.Text}
		}
		done := Event{Kind: EventDone}
		if lastUsage != nil {
			done.Usage = &Usage{
				InputTokens:  lastUsage.InputTokens,
				OutputTokens: lastUsage.OutputTokens,
				FirstTokenMs: lastUsage.FirstTokenMs,
				TotalMs:      lastUsage.TotalMs,
			}
		}
		out <- done
	}()
	return out, nil
}

// StreamAgent runs a tool-using agent run. The returned channel emits the
// same Event values as Stream plus ToolCall / ToolStream / ToolRetrieve /
// ToolResult / Permission events. Frontends respond to Permission events
// by calling Permissions().Respond(id, decision).
func (s *Session) StreamAgent(ctx context.Context, id string, req AgentRequest) (<-chan Event, error) {
	c, err := s.clientFor(req.Provider)
	if err != nil {
		return nil, err
	}
	tools, err := s.buildTools(req.ToolNames)
	if err != nil {
		return nil, err
	}
	if err := s.registerCancel(ctx, id); err != nil {
		return nil, err
	}
	runCtx, _ := s.ctxFor(id)

	approvedThisRun := make(map[string]bool, len(req.AutoApprove))
	for _, n := range req.AutoApprove {
		approvedThisRun[n] = true
	}
	var approvedMu sync.Mutex

	out := make(chan Event, 16)
	emitPermission := func(permID, name, args string) {
		out <- Event{Kind: EventPermission, Name: name, Arguments: args, PermissionID: permID}
	}
	gate := s.permissions.gate(emitPermission, approvedThisRun, &approvedMu)

	go func() {
		defer close(out)
		defer s.releaseCancel(id)

		input := toSchemaMessages(req.Messages)
		mreq := toMargoRequest(req.System, nil, req.Options, nil)
		parts := attachmentsToParts(req.Attachments)

		err := agent.RunByType(runCtx, req.RunnerType, c, mreq, tools, input, parts, gate, func(ev agent.StepEvent) {
			switch ev.Kind {
			case agent.StepText:
				out <- Event{Kind: EventText, Text: ev.Text}
			case agent.StepToolCall:
				out <- Event{Kind: EventToolCall, Name: ev.Name, Arguments: ev.Arguments}
			case agent.StepToolStream:
				out <- Event{Kind: EventToolStream, Name: ev.Name, Chunk: ev.Text}
			case agent.StepRetrieve:
				hits := make([]RetrievalHit, len(ev.Hits))
				for i, h := range ev.Hits {
					hits[i] = RetrievalHit{Path: h.Path, Doc: h.Doc, Score: h.Score, Snippet: h.Snippet}
				}
				out <- Event{Kind: EventToolRetrieve, Name: ev.Name, Hits: hits}
			case agent.StepToolResult:
				out <- Event{Kind: EventToolResult, Name: ev.Name, Result: ev.Result, IsError: ev.IsError}
			case agent.StepDone:
				done := Event{Kind: EventDone}
				if ev.Usage != nil {
					done.Usage = &Usage{
						InputTokens:  ev.Usage.InputTokens,
						OutputTokens: ev.Usage.OutputTokens,
						FirstTokenMs: ev.Usage.FirstTokenMs,
						TotalMs:      ev.Usage.TotalMs,
					}
				}
				out <- done
			case agent.StepError:
				out <- Event{Kind: EventError, Text: ev.Text}
			}
		})
		if err != nil {
			out <- Event{Kind: EventError, Err: err, Text: err.Error()}
		}
	}()
	return out, nil
}

// Cancel aborts an in-flight Stream / StreamAgent run. No-op for unknown ids.
func (s *Session) Cancel(id string) {
	s.cancelsMu.Lock()
	h, ok := s.cancels[id]
	delete(s.cancels, id)
	s.cancelsMu.Unlock()
	if ok {
		h.cancel()
	}
}

// registerCancel reserves a cancel slot for the given id. Returns an error
// if the id is already taken.
func (s *Session) registerCancel(parent context.Context, id string) error {
	ctx, cancel := context.WithCancel(parent)
	s.cancelsMu.Lock()
	defer s.cancelsMu.Unlock()
	if _, exists := s.cancels[id]; exists {
		cancel()
		return fmt.Errorf("stream id %q already in use", id)
	}
	s.cancels[id] = runHandle{ctx: ctx, cancel: cancel}
	return nil
}

// releaseCancel drops the slot and cancels the derived context. Safe to
// call after Cancel already fired.
func (s *Session) releaseCancel(id string) {
	s.cancelsMu.Lock()
	h, ok := s.cancels[id]
	delete(s.cancels, id)
	s.cancelsMu.Unlock()
	if ok {
		h.cancel()
	}
}

// ctxFor returns the derived context for a registered id. The second
// return is false when the id is unknown (run already ended).
func (s *Session) ctxFor(id string) (context.Context, bool) {
	s.cancelsMu.Lock()
	defer s.cancelsMu.Unlock()
	h, ok := s.cancels[id]
	if !ok {
		return nil, false
	}
	return h.ctx, true
}
