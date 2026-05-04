package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os/exec"
	goruntime "runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/shakfu/margo/internal/config"
	"github.com/shakfu/margo/pkg/margo"
	"github.com/shakfu/margo/pkg/margo/agent"
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

	mu          sync.Mutex
	cancels     map[string]context.CancelFunc
	permissions sync.Map // map[string]chan permissionDecision
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

// AttachmentInput carries an inline image (or future doc) attachment from
// the frontend. Data is base64-encoded so it survives the Wails JSON IPC
// without byte-array serialization quirks. Attachments are glued onto
// the latest user-role message's Parts before the request goes out;
// they are not persisted in chat history (§7.4 will revisit storage).
type AttachmentInput struct {
	Name     string `json:"name"`     // original filename, surfaced for UX only
	MimeType string `json:"mimeType"` // "image/png" / "image/jpeg" / etc.
	Data     string `json:"data"`     // base64-encoded bytes
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

// attachmentsToParts decodes the inbound attachment list into a margo
// Part slice. Used by the agent path, which threads parts through the
// adapter rather than mutating margo.Request.Messages directly. Bad
// entries (empty data, unknown mime) are silently dropped.
func attachmentsToParts(in []AttachmentInput) []margo.Part {
	if len(in) == 0 {
		return nil
	}
	out := make([]margo.Part, 0, len(in))
	for _, a := range in {
		raw, err := base64.StdEncoding.DecodeString(a.Data)
		if err != nil || len(raw) == 0 || a.MimeType == "" {
			continue
		}
		out = append(out, margo.Part{Kind: margo.PartImage, MimeType: a.MimeType, Data: raw})
	}
	return out
}

// applyAttachments glues attachments onto the final user-role message's
// Parts. The original Content string is preserved as a leading text
// part so the prompt and the image both reach the model in the same
// turn. No-op when no attachments or when the message slice is empty.
func applyAttachments(msgs []margo.Message, attachments []AttachmentInput) []margo.Message {
	if len(attachments) == 0 || len(msgs) == 0 {
		return msgs
	}
	// Find the last user-role message (index from the end). Anything
	// past it is server-generated and not a place to attach to.
	idx := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == margo.RoleUser {
			idx = i
			break
		}
	}
	if idx < 0 {
		return msgs
	}
	target := msgs[idx]
	parts := make([]margo.Part, 0, len(attachments)+1)
	if target.Content != "" {
		parts = append(parts, margo.Part{Kind: margo.PartText, Text: target.Content})
	}
	for _, a := range attachments {
		raw, err := base64.StdEncoding.DecodeString(a.Data)
		if err != nil || len(raw) == 0 || a.MimeType == "" {
			continue
		}
		parts = append(parts, margo.Part{Kind: margo.PartImage, MimeType: a.MimeType, Data: raw})
	}
	target.Parts = parts
	msgs[idx] = target
	return msgs
}

func toMargoRequest(system string, messages []ChatMessage, opts ChatOptions, attachments []AttachmentInput) margo.Request {
	msgs := toMargoMessages(messages)
	msgs = applyAttachments(msgs, attachments)
	if opts.Model != "" {
		msgs = agent.RewriteMargoForBudget(msgs, system, agent.BudgetForModel(opts.Model))
	}
	req := margo.Request{
		Model:         opts.Model,
		System:        system,
		Messages:      msgs,
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
func (a *App) Chat(provider, system string, messages []ChatMessage, opts ChatOptions, attachments []AttachmentInput) (ChatResponse, error) {
	c, err := a.clientFor(provider)
	if err != nil {
		return ChatResponse{}, err
	}
	resp, err := c.Complete(a.ctx, toMargoRequest(system, messages, opts, attachments))
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
func (a *App) StreamChat(id, provider, system string, messages []ChatMessage, opts ChatOptions, attachments []AttachmentInput) error {
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

	ch, err := c.Stream(ctx, toMargoRequest(system, messages, opts, attachments))
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

// Tools returns the list of built-in agent tools by name. The frontend uses
// this to populate the agent-mode tool picker.
func (a *App) Tools() []string {
	out := make([]string, 0, len(builtinTools))
	for name := range builtinTools {
		out = append(out, name)
	}
	return out
}

// builtinTools is the registry of tools the agent path can equip. Entries
// guarded by an availability predicate (e.g. quarto_render) are only
// registered when the underlying binary is on PATH at process start.
var builtinTools = func() map[string]func() tool.InvokableTool {
	m := map[string]func() tool.InvokableTool{
		"current_time": agent.CurrentTimeTool,
	}
	if agent.QuartoAvailable() {
		// Best-effort: if the home dir lookup fails for some reason
		// (sandboxed env, etc.), fall back to a per-call temp dir
		// rather than disabling the tool.
		dir, _ := agent.DefaultOutputDir()
		m["quarto_render"] = func() tool.InvokableTool {
			return agent.QuartoRenderTool(dir)
		}
	}
	return m
}()

// OpenPath asks the host OS to open the given local path in its default
// application (e.g. .pptx → PowerPoint, .html → default browser, dir →
// Finder/Explorer). Used for file:// links emitted by tools like
// quarto_render — Wails' built-in BrowserOpenURL rejects any scheme other
// than http(s)/mailto, so file paths need a separate path. exec.Command
// does not invoke a shell, so the path is not subject to shell-injection.
func (a *App) OpenPath(path string) error {
	if path == "" {
		return fmt.Errorf("empty path")
	}
	var cmd *exec.Cmd
	switch goruntime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}

// OutputDir returns the absolute path to margo's stable output directory
// (where create-and-render tools like quarto_render write generated
// artifacts). Bound to the frontend so the settings panel can show the
// path and offer an "open in Finder" affordance.
func (a *App) OutputDir() string {
	dir, err := agent.DefaultOutputDir()
	if err != nil {
		return ""
	}
	return dir
}

// AgentStepEvent is the payload for `margo:stream:<id>:chunk` events emitted
// during a StreamAgent run. Kind values: "text", "tool_call", "tool_result",
// "permission".
type AgentStepEvent struct {
	Kind         string `json:"kind"`
	Text         string `json:"text,omitempty"`
	Name         string `json:"name,omitempty"`
	Arguments    string `json:"arguments,omitempty"`
	Result       string `json:"result,omitempty"`
	IsError      bool   `json:"isError,omitempty"`
	PermissionID string `json:"permissionId,omitempty"`
}

// permissionDecision is the user's response to a tool-permission prompt.
type permissionDecision struct {
	approved bool
	always   bool
}

// StreamAgent runs a ReAct agent against the named tools and emits step events
// (text deltas, tool_call, tool_result) over the same `margo:stream:<id>:*`
// channels used by StreamChat.
//
// `toolNames` selects which tools from Tools() the agent can call this run;
// pass empty for plain chat (which is what StreamChat already does — prefer
// that path when no tools are needed).
func (a *App) StreamAgent(id, provider, system string, messages []ChatMessage, opts ChatOptions, toolNames []string, autoApprove []string, attachments []AttachmentInput) error {
	c, err := a.clientFor(provider)
	if err != nil {
		return err
	}

	tools := make([]tool.BaseTool, 0, len(toolNames))
	for _, name := range toolNames {
		ctor, ok := builtinTools[name]
		if !ok {
			return fmt.Errorf("unknown tool: %s", name)
		}
		tools = append(tools, ctor())
	}

	// Per-run mutable approval set: seeded from the persisted "always
	// approve" list the frontend forwards, augmented when the user clicks
	// Always on a live prompt. Not reflected back to the frontend; the
	// frontend stores its own copy in localStorage.
	approvedThisRun := make(map[string]bool, len(autoApprove))
	for _, n := range autoApprove {
		approvedThisRun[n] = true
	}
	var approvedMu sync.Mutex

	ctx, cancel := context.WithCancel(a.ctx)
	a.mu.Lock()
	if _, exists := a.cancels[id]; exists {
		a.mu.Unlock()
		cancel()
		return fmt.Errorf("stream id %q already in use", id)
	}
	a.cancels[id] = cancel
	a.mu.Unlock()

	base := "margo:stream:" + id

	gate := func(gctx context.Context, name, args string) (bool, error) {
		approvedMu.Lock()
		alreadyApproved := approvedThisRun[name]
		approvedMu.Unlock()
		if alreadyApproved {
			return true, nil
		}
		reqID := newPermissionID()
		ch := make(chan permissionDecision, 1)
		a.permissions.Store(reqID, ch)
		defer a.permissions.Delete(reqID)

		runtime.EventsEmit(a.ctx, base+":chunk", AgentStepEvent{
			Kind: "permission", Name: name, Arguments: args, PermissionID: reqID,
		})

		select {
		case <-gctx.Done():
			return false, gctx.Err()
		case d := <-ch:
			if d.always && d.approved {
				approvedMu.Lock()
				approvedThisRun[name] = true
				approvedMu.Unlock()
			}
			return d.approved, nil
		}
	}

	go func() {
		defer func() {
			a.mu.Lock()
			delete(a.cancels, id)
			a.mu.Unlock()
			cancel()
		}()

		input := toSchemaMessages(messages)
		req := toMargoRequest(system, nil, opts, nil)
		parts := attachmentsToParts(attachments)

		err := agent.StreamReact(ctx, c, req, tools, input, parts, gate, func(ev agent.StepEvent) {
			switch ev.Kind {
			case agent.StepText:
				runtime.EventsEmit(a.ctx, base+":chunk", AgentStepEvent{Kind: "text", Text: ev.Text})
			case agent.StepToolCall:
				runtime.EventsEmit(a.ctx, base+":chunk", AgentStepEvent{
					Kind: "tool_call", Name: ev.Name, Arguments: ev.Arguments,
				})
			case agent.StepToolResult:
				runtime.EventsEmit(a.ctx, base+":chunk", AgentStepEvent{
					Kind: "tool_result", Name: ev.Name, Result: ev.Result, IsError: ev.IsError,
				})
			case agent.StepDone:
				var done StreamDoneEvent
				if ev.Usage != nil {
					done.Usage = &StreamUsage{
						InputTokens:  ev.Usage.InputTokens,
						OutputTokens: ev.Usage.OutputTokens,
						FirstTokenMs: ev.Usage.FirstTokenMs,
						TotalMs:      ev.Usage.TotalMs,
					}
				}
				runtime.EventsEmit(a.ctx, base+":done", done)
			case agent.StepError:
				runtime.EventsEmit(a.ctx, base+":error", ev.Text)
			}
		})
		if err != nil {
			runtime.EventsEmit(a.ctx, base+":error", err.Error())
		}
	}()
	return nil
}

// RespondPermission delivers the user's decision on a pending tool-
// invocation permission prompt. `id` is the PermissionID that arrived in
// the originating "permission" step event. `always` only takes effect
// when `approved` is true; on Deny the field is ignored.
func (a *App) RespondPermission(id string, approved bool, always bool) error {
	v, ok := a.permissions.LoadAndDelete(id)
	if !ok {
		return fmt.Errorf("unknown permission id %q (already responded or run cancelled)", id)
	}
	v.(chan permissionDecision) <- permissionDecision{approved: approved, always: always}
	return nil
}

// newPermissionID returns a short opaque id for a permission round-trip.
// Crypto-strength randomness isn't needed — the id only has to be unique
// across the in-flight permission requests of a single process.
var permissionCounter uint64

func newPermissionID() string {
	n := atomic.AddUint64(&permissionCounter, 1)
	return fmt.Sprintf("perm-%d-%d", time.Now().UnixNano(), n)
}

func toSchemaMessages(in []ChatMessage) []*schema.Message {
	out := make([]*schema.Message, 0, len(in))
	for _, m := range in {
		role := schema.User
		if m.Role == "assistant" {
			role = schema.Assistant
		} else if m.Role == "system" {
			role = schema.System
		}
		out = append(out, &schema.Message{Role: role, Content: m.Content})
	}
	return out
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
