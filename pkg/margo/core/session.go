package core

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/shakfu/margo/pkg/margo"
	"github.com/shakfu/margo/pkg/margo/agent"
	"github.com/shakfu/margo/pkg/margo/mcp"
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

	// CatalogDir holds the per-provider model-catalog caches. Empty
	// means os.UserConfigDir; tests pass a temp directory. A cache that
	// cannot be written still works, it just refetches each launch.
	CatalogDir string

	// CatalogTTL is how long a fetched catalog is reused. Zero means
	// margo.DefaultCatalogTTL.
	CatalogTTL time.Duration

	// AllowPrivateNetwork lets web_fetch reach loopback and LAN
	// addresses. Off by default: the URL is model-chosen, so reaching
	// the user's own network is a decision only the user can make.
	AllowPrivateNetwork bool

	// QuartoExecute enables Quarto's computational cells in the
	// quarto_render tool. Off by default: the document body is
	// model-authored, so executing it is arbitrary code execution on
	// the user's machine. See agent.QuartoOptions.Execute.
	QuartoExecute bool

	// MCPConfig is the parsed Claude-Desktop-compatible mcpServers
	// catalog. When non-nil and non-empty, NewSession starts every
	// server eagerly and asynchronously — handshake/listTools runs in
	// per-server goroutines so a slow or broken server does not delay
	// Session construction. The front-end observes per-server
	// status via Session.MCP().Servers().
	MCPConfig mcp.Config

	// MCPLogger, when non-nil, is the destination for MCP lifecycle
	// events and per-server stderr lines. Pass log.Default() for the
	// usual stdout/stderr; pass a file-backed logger to keep MCP noise
	// out of the user's terminal.
	MCPLogger *log.Logger
}

// Session is margo's UI-agnostic orchestration root: it owns the
// configured provider clients, the workspace registry, the attachment
// store, the permission broker, and the per-stream cancel registry.
//
// Streaming methods return <-chan Event. Each front-end consumes the
// channel and translates events to its own transport (a desktop event
// emit, a terminal-UI message, SSE, …).
type Session struct {
	anthropic  margo.Client
	openai     margo.Client
	openrouter margo.Client

	workspaces  *WorkspaceRegistry
	attachments *AttachmentStore
	permissions *PermissionBroker
	mcp         *mcp.Manager
	catalog     *margo.CatalogCache

	cancelsMu sync.Mutex
	cancels   map[string]runHandle

	// quartoExecute and allowPrivateNetwork are read by the tool
	// constructors in tools.go on every buildTools call.
	quartoExecute       bool
	allowPrivateNetwork bool
}

// runHandle bundles the per-stream cancel function with the derived
// context so workers can read both off the registry.
type runHandle struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// mcpShutdownTimeout is the per-server grace window between SIGTERM
// (implicit when we close stdin) and SIGKILL on Session.Shutdown.
// Conservative — community servers are mostly node.js processes that
// exit promptly on EOF, but a slow shutdown is preferable to a kill -9
// that leaves temp files around.
const mcpShutdownTimeout = 3 * time.Second

// NewSession builds a Session from the given Config. Unconfigured
// providers (missing API key) simply do not appear in Providers().
//
// MCP servers listed in cfg.MCPConfig are launched eagerly + async:
// NewSession returns immediately; per-server initialize/listTools
// runs in goroutines. The caller observes readiness via
// Session.MCP().Servers()[i].Status().
func NewSession(cfg Config) *Session {
	catalogDir := cfg.CatalogDir
	if catalogDir == "" {
		// A missing config directory is not fatal: the cache falls back
		// to memory-only and the embedded seed still serves models.
		catalogDir, _ = margo.DefaultCatalogDir()
	}
	catalog := margo.NewCatalogCache(catalogDir, cfg.CatalogTTL)
	// Installed process-wide so agent.BudgetForModel resolves context
	// windows against live data rather than the build-time seed.
	margo.SetActiveCatalog(catalog)

	s := &Session{
		catalog:             catalog,
		workspaces:          NewWorkspaceRegistry(cfg.OpenAIAPIKey),
		attachments:         NewAttachmentStore(cfg.AttachmentRoot),
		permissions:         NewPermissionBroker(),
		mcp:                 mcp.NewManager(cfg.MCPLogger),
		cancels:             map[string]runHandle{},
		quartoExecute:       cfg.QuartoExecute,
		allowPrivateNetwork: cfg.AllowPrivateNetwork,
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
	// Eager + async server start. context.Background is intentional —
	// MCP server lifetime is tied to the Session, not to any one call.
	// Each server's startServer runs in its own goroutine via the
	// manager's addAndStart so we don't block here.
	s.mcp.StartAll(context.Background(), cfg.MCPConfig)
	return s
}

// Workspaces exposes the registry for IndexPath / Sources / SetActive
// calls from the front-end.
func (s *Session) Workspaces() *WorkspaceRegistry { return s.workspaces }

// Attachments exposes the attachment store.
func (s *Session) Attachments() *AttachmentStore { return s.attachments }

// Permissions exposes the permission broker so the front-end can call
// Respond when the user accepts or rejects a prompt.
func (s *Session) Permissions() *PermissionBroker { return s.permissions }

// MCP exposes the MCP manager so front-ends can list servers, add/remove
// servers at runtime, and observe per-server status (including the
// stderr ring buffer for failure diagnostics).
func (s *Session) MCP() *mcp.Manager { return s.mcp }

// Shutdown stops every managed MCP server in parallel. Call from the
// host application's exit path; safe to call multiple times. The
// per-server stop timeout matches the manager default tradeoff —
// polite SIGTERM first, SIGKILL after 3 seconds.
func (s *Session) Shutdown() {
	if s.mcp != nil {
		s.mcp.StopAll(mcpShutdownTimeout)
	}
}

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

// Models returns the model identifiers exposed for a provider, in
// catalog order: curated entries from the embedded models.json first,
// then anything else the provider reported, alphabetically. First entry
// is the default. See RefreshModels for where the list comes from.
func (s *Session) Models(provider string) []string {
	return s.catalog.ModelIDs(provider)
}

// Catalog returns the full model catalog. Exposed so front-ends can
// retire their own hand-mirrored model lists (context-window sizes,
// multimodal capability) and read this single source of truth instead.
func (s *Session) Catalog() margo.Catalog {
	return s.catalog.Catalog()
}

// RefreshModels fetches provider's model list and stores it. Returns an
// error when the provider has no key, cannot list models, or the fetch
// fails; in every failure case the previous catalog stays in place, so a
// failed refresh degrades to stale data rather than to an empty picker.
func (s *Session) RefreshModels(ctx context.Context, provider string) error {
	c, err := s.clientFor(provider)
	if err != nil {
		return err
	}
	lister, ok := c.(margo.ModelLister)
	if !ok {
		return fmt.Errorf("provider %q does not support listing models", provider)
	}
	return s.catalog.Refresh(ctx, provider, lister)
}

// RefreshStaleModels refreshes every configured provider whose catalog
// has aged past the TTL, in parallel. Errors are returned per provider
// and are advisory: the caller shows them, the app keeps working.
//
// Intended to be called once at startup on a background goroutine. It
// is not called from NewSession because a constructor that makes
// network calls is a constructor that hangs.
func (s *Session) RefreshStaleModels(ctx context.Context) map[string]error {
	errs := map[string]error{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, provider := range s.Providers() {
		p := provider
		c, err := s.clientFor(p)
		if err != nil {
			continue
		}
		lister, ok := c.(margo.ModelLister)
		if !ok {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.catalog.RefreshIfStale(ctx, p, lister); err != nil {
				mu.Lock()
				errs[p] = err
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return errs
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
	runCtx, err := s.registerCancel(ctx, id)
	if err != nil {
		return nil, err
	}

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
	runCtx, err := s.registerCancel(ctx, id)
	if err != nil {
		return nil, err
	}

	// A persisted grant is only as good as the current policy: a tool
	// that has since joined NoAlwaysApproveTools must prompt again even
	// though the front-end still has it on the pre-authorised list.
	approvedThisRun := make(map[string]bool, len(req.AutoApprove))
	for _, n := range req.AutoApprove {
		if agent.AllowsAlwaysApprove(n) {
			approvedThisRun[n] = true
		}
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

// registerCancel reserves a cancel slot for the given id and returns the
// derived context the run should use. Errors if the id is already taken.
//
// The context comes back from inside the critical section rather than
// from a follow-up lookup, because the two-step version raced: a Cancel
// landing between the register and the lookup deleted the entry, the
// lookup returned a nil context, and that nil reached the provider
// client where the first ctx.Done() panicked. Pressing send and cancel
// in quick succession was enough to hit it. Cancellation still
// propagates through the returned context once the map entry is gone.
func (s *Session) registerCancel(parent context.Context, id string) (context.Context, error) {
	ctx, cancel := context.WithCancel(parent)
	s.cancelsMu.Lock()
	defer s.cancelsMu.Unlock()
	if _, exists := s.cancels[id]; exists {
		cancel()
		return nil, fmt.Errorf("stream id %q already in use", id)
	}
	s.cancels[id] = runHandle{ctx: ctx, cancel: cancel}
	return ctx, nil
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
