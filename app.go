package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os/exec"
	goruntime "runtime"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/shakfu/margo/internal/config"
	"github.com/shakfu/margo/pkg/margo/agent"
	"github.com/shakfu/margo/pkg/margo/core"
)

// App is the Wails-bound struct. Exported methods are callable from the
// frontend via the auto-generated bindings in
// frontend/wailsjs/go/main/App.{js,d.ts}.
//
// App is intentionally thin: it holds a *core.Session and translates
// between the Wails wire format (JSON via auto-generated bindings) and
// the core's UI-agnostic API (Go channels of core.Event values).
// All business logic — provider routing, agent runners, workspace
// management, attachment storage, permission brokering — lives in
// pkg/margo/core.
type App struct {
	ctx     context.Context
	cfg     *config.Config
	session *core.Session

	// startupWorkspaceDir is set by main() from the -workspace CLI flag.
	// Read once on the frontend's first paint via StartupWorkspaceDir.
	startupWorkspaceDir string
}

func NewApp() *App {
	cfg, _ := config.Load()
	return &App{
		cfg: cfg,
		session: core.NewSession(core.Config{
			AnthropicAPIKey:  cfg.AnthropicAPIKey,
			OpenAIAPIKey:     cfg.OpenAIAPIKey,
			OpenRouterAPIKey: cfg.OpenRouterAPIKey,
		}),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// Greet is the stock Wails template method, retained for reference.
func (a *App) Greet(name string) string {
	return fmt.Sprintf("Hello %s, It's show time!", name)
}

// Providers returns the list of providers that have an API key configured.
func (a *App) Providers() []string { return a.session.Providers() }

// Models returns the list of model identifiers we expose for a provider.
func (a *App) Models(provider string) []string { return a.session.Models(provider) }

// ChatMessage mirrors core.Message for JSON binding to the frontend.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// AttachmentInput carries an inline image/document attachment from the
// frontend. Data is base64-encoded so it survives the Wails JSON IPC.
// Decoded at the Wails boundary into core.Attachment (raw bytes).
type AttachmentInput struct {
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// StoredAttachment is the JSON-tagged Wails view of core.StoredAttachment.
type StoredAttachment = core.StoredAttachment

// ChatOptions mirrors core.Options for JSON binding to the frontend.
type ChatOptions struct {
	Model         string   `json:"model"`
	MaxTokens     int      `json:"maxTokens"`
	Temperature   *float64 `json:"temperature"`
	TopP          *float64 `json:"topP"`
	StopSequences []string `json:"stopSequences"`
	ThinkEnabled  bool     `json:"thinkEnabled"`
	ThinkBudget   int      `json:"thinkBudget"`
}

// StreamChunkEvent is the payload for `margo:stream:<id>:chunk` (chat path).
type StreamChunkEvent struct {
	Kind string `json:"kind"` // "text" | "thinking"
	Text string `json:"text"`
}

// StreamUsage mirrors core.Usage for JSON binding.
type StreamUsage struct {
	InputTokens  int   `json:"inputTokens"`
	OutputTokens int   `json:"outputTokens"`
	FirstTokenMs int64 `json:"firstTokenMs"`
	TotalMs      int64 `json:"totalMs"`
}

// StreamDoneEvent is the payload for `margo:stream:<id>:done`.
type StreamDoneEvent struct {
	Usage *StreamUsage `json:"usage"`
}

// ChatResponse is the non-streaming completion result.
type ChatResponse struct {
	Text     string      `json:"text"`
	Thinking string      `json:"thinking"`
	Model    string      `json:"model"`
	Usage    StreamUsage `json:"usage"`
}

// ToolMetadata is the JSON-tagged Wails view of core.ToolMetadata.
type ToolMetadata = core.ToolMetadata

// IndexResult is the JSON-tagged Wails view of core.IndexResult.
type IndexResult = core.IndexResult

// KnowledgeSource is the JSON-tagged Wails view of core.KnowledgeSource.
type KnowledgeSource = core.KnowledgeSource

// AgentStepEvent is the payload for `margo:stream:<id>:chunk` (agent path).
type AgentStepEvent struct {
	Kind         string         `json:"kind"`
	Text         string         `json:"text,omitempty"`
	Name         string         `json:"name,omitempty"`
	Arguments    string         `json:"arguments,omitempty"`
	Result       string         `json:"result,omitempty"`
	IsError      bool           `json:"isError,omitempty"`
	PermissionID string         `json:"permissionId,omitempty"`
	Chunk        string         `json:"chunk,omitempty"`
	Hits         []RetrievalHit `json:"hits,omitempty"`
}

// RetrievalHit is the JSON-tagged Wails view of core.RetrievalHit.
type RetrievalHit = core.RetrievalHit

// inputsToCoreAttachments decodes base64 payloads at the Wails boundary
// so core sees raw bytes. Bad entries (empty data, undecodable) are
// silently dropped to match prior behavior.
func inputsToCoreAttachments(in []AttachmentInput) []core.Attachment {
	if len(in) == 0 {
		return nil
	}
	out := make([]core.Attachment, 0, len(in))
	for _, a := range in {
		if a.MimeType == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(a.Data)
		if err != nil || len(raw) == 0 {
			continue
		}
		out = append(out, core.Attachment{Name: a.Name, MimeType: a.MimeType, Data: raw})
	}
	return out
}

func toCoreMessages(in []ChatMessage) []core.Message {
	out := make([]core.Message, len(in))
	for i, m := range in {
		out[i] = core.Message{Role: m.Role, Content: m.Content}
	}
	return out
}

func toCoreOptions(o ChatOptions) core.Options {
	return core.Options{
		Model:         o.Model,
		MaxTokens:     o.MaxTokens,
		Temperature:   o.Temperature,
		TopP:          o.TopP,
		StopSequences: o.StopSequences,
		ThinkEnabled:  o.ThinkEnabled,
		ThinkBudget:   o.ThinkBudget,
	}
}

func toStreamUsage(u *core.Usage) *StreamUsage {
	if u == nil {
		return nil
	}
	return &StreamUsage{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		FirstTokenMs: u.FirstTokenMs,
		TotalMs:      u.TotalMs,
	}
}

// Chat performs a non-streaming multi-turn completion.
func (a *App) Chat(provider, system string, messages []ChatMessage, opts ChatOptions, attachments []AttachmentInput) (ChatResponse, error) {
	resp, err := a.session.Chat(a.ctx, core.ChatRequest{
		Provider:    provider,
		System:      system,
		Messages:    toCoreMessages(messages),
		Options:     toCoreOptions(opts),
		Attachments: inputsToCoreAttachments(attachments),
	})
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

// StreamChat starts a streaming completion. Events emitted:
//
//	margo:stream:<id>:chunk  payload = StreamChunkEvent {kind, text}
//	margo:stream:<id>:error  payload = string
//	margo:stream:<id>:done   payload = StreamDoneEvent {usage}
//
// Cancel an in-flight stream with CancelStream(id).
func (a *App) StreamChat(id, provider, system string, messages []ChatMessage, opts ChatOptions, attachments []AttachmentInput) error {
	ch, err := a.session.Stream(a.ctx, id, core.ChatRequest{
		Provider:    provider,
		System:      system,
		Messages:    toCoreMessages(messages),
		Options:     toCoreOptions(opts),
		Attachments: inputsToCoreAttachments(attachments),
	})
	if err != nil {
		return err
	}
	base := "margo:stream:" + id
	go func() {
		for ev := range ch {
			switch ev.Kind {
			case core.EventText:
				runtime.EventsEmit(a.ctx, base+":chunk", StreamChunkEvent{Kind: "text", Text: ev.Text})
			case core.EventThinking:
				runtime.EventsEmit(a.ctx, base+":chunk", StreamChunkEvent{Kind: "thinking", Text: ev.Text})
			case core.EventDone:
				runtime.EventsEmit(a.ctx, base+":done", StreamDoneEvent{Usage: toStreamUsage(ev.Usage)})
			case core.EventError:
				runtime.EventsEmit(a.ctx, base+":error", ev.Text)
			}
		}
	}()
	return nil
}

// Tools returns the list of built-in agent tools by name.
func (a *App) Tools() []string { return a.session.Tools() }

// ToolsMetadata returns one ToolMetadata per registered tool, sorted by name.
func (a *App) ToolsMetadata() []ToolMetadata { return a.session.ToolsMetadata(a.ctx) }

// OpenPath asks the host OS to open the given local path in its default
// application. Used for file:// links emitted by tools — Wails'
// BrowserOpenURL rejects non-http(s)/mailto schemes.
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

// openSettings is the menu callback for Margo › Settings…
func (a *App) openSettings() {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, "margo:menu:settings")
}

// StartupWorkspaceDir returns the workspace directory the frontend should
// attach to on first paint (populated from the -workspace CLI flag).
func (a *App) StartupWorkspaceDir() string { return a.startupWorkspaceDir }

// PickWorkspaceDir opens the OS native directory picker.
func (a *App) PickWorkspaceDir() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Choose workspace directory",
	})
}

// OutputDir returns the absolute path to margo's stable output directory.
func (a *App) OutputDir() string {
	dir, err := agent.DefaultOutputDir()
	if err != nil {
		return ""
	}
	return dir
}

// SetActiveWorkspace records which workspace the frontend has in focus.
func (a *App) SetActiveWorkspace(id string) { a.session.Workspaces().SetActive(id) }

// IndexPath indexes a file or directory into the workspace's collection.
func (a *App) IndexPath(workspaceID, path string) (IndexResult, error) {
	return a.session.Workspaces().IndexPath(a.ctx, workspaceID, path)
}

// KnowledgeSources lists what's currently indexed.
func (a *App) KnowledgeSources(workspaceID string) []KnowledgeSource {
	return a.session.Workspaces().Sources(workspaceID)
}

// DeleteKnowledgeSource drops every chunk that belongs to a source path.
func (a *App) DeleteKnowledgeSource(workspaceID, path string) error {
	return a.session.Workspaces().DeleteSource(a.ctx, workspaceID, path)
}

// PickKnowledgePath opens the OS native picker for a file or directory.
func (a *App) PickKnowledgePath(dirOnly bool) (string, error) {
	if dirOnly {
		return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
			Title: "Choose a folder to index",
		})
	}
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Choose a file to index",
	})
}

// SaveAttachment writes a base64-encoded blob to disk under a per-chat dir.
// Base64 decoding happens at the Wails boundary; core stores raw bytes.
func (a *App) SaveAttachment(chatID, name, mimeType, data string) (StoredAttachment, error) {
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return StoredAttachment{}, fmt.Errorf("decode: %w", err)
	}
	return a.session.Attachments().Save(chatID, name, mimeType, raw)
}

// LoadAttachment reads a stored blob back as a base64 string for re-feeding
// into AttachmentInput.
func (a *App) LoadAttachment(path string) (string, error) {
	raw, err := a.session.Attachments().Load(path)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// DeleteChatAttachments removes every blob stored for the given chat.
func (a *App) DeleteChatAttachments(chatID string) error {
	return a.session.Attachments().DeleteChat(chatID)
}

// StreamAgent runs a tool-using agent against the named tools.
//
// runnerType is the slash-command runner identifier ("react", "plan",
// "workflow"). Empty string defaults to ReAct.
func (a *App) StreamAgent(id, provider, system string, messages []ChatMessage, opts ChatOptions, toolNames []string, autoApprove []string, attachments []AttachmentInput, runnerType string) error {
	ch, err := a.session.StreamAgent(a.ctx, id, core.AgentRequest{
		ChatRequest: core.ChatRequest{
			Provider:    provider,
			System:      system,
			Messages:    toCoreMessages(messages),
			Options:     toCoreOptions(opts),
			Attachments: inputsToCoreAttachments(attachments),
		},
		ToolNames:   toolNames,
		AutoApprove: autoApprove,
		RunnerType:  runnerType,
	})
	if err != nil {
		return err
	}
	base := "margo:stream:" + id
	go func() {
		for ev := range ch {
			switch ev.Kind {
			case core.EventText:
				runtime.EventsEmit(a.ctx, base+":chunk", AgentStepEvent{Kind: "text", Text: ev.Text})
			case core.EventToolCall:
				runtime.EventsEmit(a.ctx, base+":chunk", AgentStepEvent{Kind: "tool_call", Name: ev.Name, Arguments: ev.Arguments})
			case core.EventToolStream:
				runtime.EventsEmit(a.ctx, base+":chunk", AgentStepEvent{Kind: "tool_stream", Name: ev.Name, Chunk: ev.Chunk})
			case core.EventToolRetrieve:
				runtime.EventsEmit(a.ctx, base+":chunk", AgentStepEvent{Kind: "tool_retrieve", Name: ev.Name, Hits: ev.Hits})
			case core.EventToolResult:
				runtime.EventsEmit(a.ctx, base+":chunk", AgentStepEvent{Kind: "tool_result", Name: ev.Name, Result: ev.Result, IsError: ev.IsError})
			case core.EventPermission:
				runtime.EventsEmit(a.ctx, base+":chunk", AgentStepEvent{Kind: "permission", Name: ev.Name, Arguments: ev.Arguments, PermissionID: ev.PermissionID})
			case core.EventDone:
				runtime.EventsEmit(a.ctx, base+":done", StreamDoneEvent{Usage: toStreamUsage(ev.Usage)})
			case core.EventError:
				runtime.EventsEmit(a.ctx, base+":error", ev.Text)
			}
		}
	}()
	return nil
}

// RespondPermission delivers the user's decision on a pending prompt.
func (a *App) RespondPermission(id string, approved bool, always bool) error {
	return a.session.Permissions().Respond(id, core.PermissionDecision{Approved: approved, Always: always})
}

// CancelStream cancels an in-flight stream. No-op if the id is unknown.
func (a *App) CancelStream(id string) { a.session.Cancel(id) }
