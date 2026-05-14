package main

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
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
	"github.com/shakfu/margo/pkg/margo/rag"
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

	// startupWorkspaceDir is set by main() from the -workspace CLI flag
	// (7.1.e). Read once on the frontend's first paint via the
	// StartupWorkspaceDir Wails binding; not consumed Go-side.
	startupWorkspaceDir string

	// RAG: per-workspace indexers, created lazily on first IndexPath /
	// search_knowledge invocation. activeWorkspaceID is set by the
	// frontend via SetActiveWorkspace so the search_knowledge tool — a
	// zero-arg constructor at registration time — can resolve which
	// collection to query at run time.
	ragMu             sync.Mutex
	ragIndexers       map[string]*rag.Indexer
	activeWorkspaceID string
}

func NewApp() *App {
	cfg, _ := config.Load()
	a := &App{
		cfg:         cfg,
		cancels:     map[string]context.CancelFunc{},
		ragIndexers: map[string]*rag.Indexer{},
	}
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
// the latest user-role message's Parts before the request goes out.
//
// Attachments are persisted via SaveAttachment (§7.4) — the frontend
// writes bytes to a per-chat directory on disk, stores the returned
// path on the Message, and reloads bytes via LoadAttachment when
// re-sending a stored chat.
type AttachmentInput struct {
	Name     string `json:"name"`     // original filename, surfaced for UX only
	MimeType string `json:"mimeType"` // "image/png" / "image/jpeg" / etc.
	Data     string `json:"data"`     // base64-encoded bytes
}

// StoredAttachment is what SaveAttachment returns to the frontend: enough
// to identify the on-disk blob and reconstruct an AttachmentInput later
// via LoadAttachment. Persisted in localStorage as part of Message so
// chat history survives a reload.
type StoredAttachment struct {
	Path     string `json:"path"`     // absolute on-disk path
	Name     string `json:"name"`     // original filename
	MimeType string `json:"mimeType"` // e.g. "image/png", "application/pdf"
	Size     int64  `json:"size"`     // raw byte count
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
// entries (empty data, unknown mime) are silently dropped. MIME types
// starting with "image/" become PartImage; everything else becomes
// PartDocument so PDFs and other docs flow to the provider's document
// path (§7.5).
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
		kind := margo.PartDocument
		if strings.HasPrefix(a.MimeType, "image/") {
			kind = margo.PartImage
		}
		out = append(out, margo.Part{Kind: kind, MimeType: a.MimeType, Data: raw, Name: a.Name})
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
		kind := margo.PartDocument
		if strings.HasPrefix(a.MimeType, "image/") {
			kind = margo.PartImage
		}
		parts = append(parts, margo.Part{Kind: kind, MimeType: a.MimeType, Data: raw, Name: a.Name})
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

// ToolMetadata is the descriptive payload the frontend Tools tab
// (TODO §9.3) reads to render the read-only tool catalog: the
// model-facing description, the read-only flag that gates the
// permission middleware, and whether the underlying type
// implements tool.StreamableTool so the UI can render the chunk
// stream when the tool is invoked.
type ToolMetadata struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	IsReadOnly   bool   `json:"isReadOnly"`
	IsStreamable bool   `json:"isStreamable"`
}

// ToolsMetadata returns one ToolMetadata per registered tool, sorted by
// name for deterministic UI rendering. Each tool is constructed once
// to read its Info() and type-asserted for tool.StreamableTool; the
// `*App` argument is whatever per-run state the constructor closes
// over (the search_knowledge tool's indexer, for example), but we
// don't invoke the tool — only inspect it.
func (a *App) ToolsMetadata() []ToolMetadata {
	out := make([]ToolMetadata, 0, len(builtinTools))
	for name, ctor := range builtinTools {
		t := ctor(a)
		desc := ""
		if info, err := t.Info(a.ctx); err == nil && info != nil {
			desc = info.Desc
		}
		_, streamable := t.(tool.StreamableTool)
		out = append(out, ToolMetadata{
			Name:         name,
			Description:  desc,
			IsReadOnly:   agent.ReadOnlyTools[name],
			IsStreamable: streamable,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// builtinTools is the registry of tools the agent path can equip. Entries
// guarded by an availability predicate (e.g. quarto_render) are only
// registered when the underlying binary is on PATH at process start.
// toolCtor builds a tool for a given run. The *App argument lets a tool
// close over per-app state (active workspace, RAG indexers, etc.) that is
// only known at run time rather than package-init time.
type toolCtor func(*App) tool.BaseTool

var builtinTools = func() map[string]toolCtor {
	m := map[string]toolCtor{
		"current_time":     func(*App) tool.BaseTool { return agent.CurrentTimeTool() },
		"web_fetch":        func(*App) tool.BaseTool { return agent.WebFetchTool() },
		"search_knowledge": func(a *App) tool.BaseTool { return agent.SearchKnowledgeTool(a.activeIndexer()) },
	}
	if agent.QuartoAvailable() {
		// Best-effort: if the home dir lookup fails for some reason
		// (sandboxed env, etc.), fall back to a per-call temp dir
		// rather than disabling the tool.
		dir, _ := agent.DefaultOutputDir()
		m["quarto_render"] = func(*App) tool.BaseTool {
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

// openSettings is the callback for the Margo › Settings… menu item.
// Emits an event the frontend listens for to open the settings dialog.
// Lower-case (unexported) on purpose — this is invoked by Wails' menu
// runtime, not by the JS bindings.
func (a *App) openSettings() {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, "margo:menu:settings")
}

// StartupWorkspaceDir returns the workspace directory the frontend
// should attach to on first paint, populated from the -workspace CLI
// flag in main(). Empty string means "no startup workspace requested".
// Read once at boot; subsequent calls return the same value (we don't
// clear it — re-reads are harmless and the frontend gates with a flag).
// (7.1.e)
func (a *App) StartupWorkspaceDir() string {
	return a.startupWorkspaceDir
}

// PickWorkspaceDir opens the OS native directory picker and returns the
// selected absolute path. Returns the empty string when the user cancels
// (Wails returns "" for OpenDirectoryDialog cancellation rather than an
// error). Used by the workspace manager (left pane) to attach a folder
// to a workspace; the path is stored client-side and not consumed by Go
// in 7.1.a — later slices (RAG, file context) will plug into it.
func (a *App) PickWorkspaceDir() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Choose workspace directory",
	})
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

// SetActiveWorkspace records which workspace the frontend currently has in
// focus. The search_knowledge tool reads this at invoke time to resolve
// which collection to query. Called by the frontend on initial load and
// whenever the user switches workspace; no-op if the id is unchanged.
func (a *App) SetActiveWorkspace(id string) {
	a.ragMu.Lock()
	a.activeWorkspaceID = id
	a.ragMu.Unlock()
}

// indexerFor returns the workspace's Indexer, creating it on first call.
// Returns nil when no OpenAI key is configured (the embedder cannot run);
// callers must handle nil rather than treating it as an error since the
// path is hot on every search_knowledge invocation.
func (a *App) indexerFor(workspaceID string) *rag.Indexer {
	if workspaceID == "" {
		return nil
	}
	a.ragMu.Lock()
	defer a.ragMu.Unlock()
	if idx, ok := a.ragIndexers[workspaceID]; ok {
		return idx
	}
	if a.cfg == nil || a.cfg.OpenAIAPIKey == "" {
		return nil
	}
	dir, err := rag.WorkspaceVectorDir(workspaceID)
	if err != nil {
		return nil
	}
	emb := rag.NewOpenAIEmbedder(a.cfg.OpenAIAPIKey)
	store, err := rag.NewChromemStore(dir, workspaceID, emb.Dimensions())
	if err != nil {
		return nil
	}
	idx, err := rag.NewIndexer(emb, store, rag.IndexerOptions{
		SourcesPath: filepath.Join(dir, "sources.json"),
	})
	if err != nil {
		return nil
	}
	a.ragIndexers[workspaceID] = idx
	return idx
}

// activeIndexer is the search_knowledge tool's entry point. Called when a
// run starts; returns nil if no workspace is active or no embedder is
// available — the tool surfaces that to the model as a clear "not
// configured" string rather than erroring.
func (a *App) activeIndexer() *rag.Indexer {
	a.ragMu.Lock()
	id := a.activeWorkspaceID
	a.ragMu.Unlock()
	return a.indexerFor(id)
}

// IndexResult mirrors rag.IndexResult for the Wails binding. Kept as a
// separate struct so the JSON shape is owned by the app layer rather than
// leaking the package-internal type.
type IndexResult struct {
	Path       string `json:"path"`
	FileCount  int    `json:"fileCount"`
	ChunkCount int    `json:"chunkCount"`
}

// KnowledgeSource mirrors rag.SourceInfo for the Wails binding.
type KnowledgeSource struct {
	Path       string `json:"path"`
	IsDir      bool   `json:"isDir"`
	FileCount  int    `json:"fileCount"`
	ChunkCount int    `json:"chunkCount"`
	IndexedAt  string `json:"indexedAt"`
}

// IndexPath indexes a file or directory into the given workspace's
// knowledge collection. Returns the per-source file / chunk counts.
// Errors when no OpenAI key is configured (the embedder requires it).
func (a *App) IndexPath(workspaceID, path string) (IndexResult, error) {
	if workspaceID == "" {
		return IndexResult{}, fmt.Errorf("workspace id is required")
	}
	idx := a.indexerFor(workspaceID)
	if idx == nil {
		return IndexResult{}, fmt.Errorf("knowledge indexing requires OPENAI_API_KEY (used for embeddings)")
	}
	r, err := idx.IndexPath(a.ctx, path)
	if err != nil {
		return IndexResult{}, err
	}
	return IndexResult{Path: r.Path, FileCount: r.FileCount, ChunkCount: r.ChunkCount}, nil
}

// KnowledgeSources lists what's currently indexed in the given workspace.
// Returns an empty slice (not nil, so the JS side gets `[]`) when no
// indexer has been created yet — that's the steady state for users who
// haven't indexed anything.
func (a *App) KnowledgeSources(workspaceID string) []KnowledgeSource {
	out := []KnowledgeSource{}
	if workspaceID == "" {
		return out
	}
	idx := a.indexerFor(workspaceID)
	if idx == nil {
		return out
	}
	for _, s := range idx.Sources() {
		out = append(out, KnowledgeSource{
			Path:       s.Path,
			IsDir:      s.IsDir,
			FileCount:  s.FileCount,
			ChunkCount: s.ChunkCount,
			IndexedAt:  s.IndexedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

// DeleteKnowledgeSource drops every chunk that belongs to the given source
// path from the workspace's collection.
func (a *App) DeleteKnowledgeSource(workspaceID, path string) error {
	if workspaceID == "" {
		return fmt.Errorf("workspace id is required")
	}
	idx := a.indexerFor(workspaceID)
	if idx == nil {
		return fmt.Errorf("no knowledge index for workspace %q", workspaceID)
	}
	return idx.DeleteSource(a.ctx, path)
}

// PickKnowledgePath opens the OS native picker for a file or directory.
// Returns the empty string when the user cancels. Used by the Knowledge
// Sources panel to add a new path to the index.
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

// attachmentsRoot returns the on-disk root where SaveAttachment writes
// blobs. One subdirectory per chat. Created lazily by SaveAttachment.
func (a *App) attachmentsRoot() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("user config dir: %w", err)
	}
	return filepath.Join(cfg, "Margo", "attachments"), nil
}

// attachmentSafeBase strips path separators and other risky characters
// from a user-supplied filename so it can land on disk under a known
// directory without escaping it. Keeps a recognisable suffix where
// possible — base name + lowercase extension.
func attachmentSafeBase(name string) string {
	name = filepath.Base(name)
	// filepath.Base on Windows-style "C:\foo" returns "foo" only on
	// Windows builds; on darwin/linux the backslash is treated as a
	// literal byte. Strip both separator flavours defensively.
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "attachment"
	}
	if len(name) > 80 {
		ext := filepath.Ext(name)
		if len(ext) > 16 {
			ext = ""
		}
		name = name[:80-len(ext)] + ext
	}
	return name
}

// validateChatID rejects ids that would let an attacker write outside the
// per-chat attachments subtree (e.g. "..", "../foo"). Chat ids in the
// frontend are crypto.randomUUID() — alphanumerics + dashes — but the
// Wails surface is callable from anywhere in the page, so validate.
func validateChatID(id string) error {
	if id == "" {
		return fmt.Errorf("chat id is required")
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return fmt.Errorf("invalid chat id %q", id)
	}
	return nil
}

// SaveAttachment writes a base64-encoded blob to the per-chat attachments
// directory and returns its on-disk record. Callers (the frontend) persist
// the StoredAttachment.Path on the message; LoadAttachment reverses the
// process when re-sending.
//
// Filenames are de-collided with a timestamp + random suffix; the original
// name is preserved as a UX-facing label on the StoredAttachment. Path
// traversal in `name` is neutralised by attachmentSafeBase.
func (a *App) SaveAttachment(chatID, name, mimeType, data string) (StoredAttachment, error) {
	if err := validateChatID(chatID); err != nil {
		return StoredAttachment{}, err
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return StoredAttachment{}, fmt.Errorf("decode: %w", err)
	}
	if len(raw) == 0 {
		return StoredAttachment{}, fmt.Errorf("empty attachment")
	}
	root, err := a.attachmentsRoot()
	if err != nil {
		return StoredAttachment{}, err
	}
	dir := filepath.Join(root, chatID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return StoredAttachment{}, fmt.Errorf("mkdir: %w", err)
	}
	safe := attachmentSafeBase(name)
	stamp := time.Now().UnixNano()
	// 6 random bytes encoded as 12 hex chars — enough entropy that the
	// timestamp suffix isn't load-bearing for uniqueness.
	rndBuf := make([]byte, 6)
	if _, err := io.ReadFull(cryptorand.Reader, rndBuf); err != nil {
		return StoredAttachment{}, fmt.Errorf("rand: %w", err)
	}
	filename := fmt.Sprintf("%d-%x-%s", stamp, rndBuf, safe)
	abs := filepath.Join(dir, filename)
	if err := os.WriteFile(abs, raw, 0o644); err != nil {
		return StoredAttachment{}, fmt.Errorf("write: %w", err)
	}
	return StoredAttachment{
		Path:     abs,
		Name:     name,
		MimeType: mimeType,
		Size:     int64(len(raw)),
	}, nil
}

// LoadAttachment reads a stored blob back as a base64 string suitable for
// re-feeding into AttachmentInput. Validates that the path lives under
// the attachments root so this method cannot be turned into an arbitrary
// file reader from the frontend.
func (a *App) LoadAttachment(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	root, err := a.attachmentsRoot()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("abs: %w", err)
	}
	// Reject paths that don't live inside the attachments root. filepath.Rel
	// returning a "..-prefixed" result means abs escaped root.
	rel, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(rel, "..") || strings.Contains(rel, "..") {
		return "", fmt.Errorf("path outside attachments root")
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// DeleteChatAttachments removes every blob stored for the given chat.
// Called by the frontend when the chat itself is deleted so attachments
// don't accumulate on disk forever. Missing dirs are not an error
// (idempotent).
func (a *App) DeleteChatAttachments(chatID string) error {
	if err := validateChatID(chatID); err != nil {
		return err
	}
	root, err := a.attachmentsRoot()
	if err != nil {
		return err
	}
	dir := filepath.Join(root, chatID)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("delete chat attachments: %w", err)
	}
	return nil
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
	// Chunk carries an incremental piece of a streamable tool's output. Set
	// only for Kind=="tool_stream" events; concatenating all chunks for a
	// given tool_call reconstructs what the matching tool_result Result
	// will hold once the stream ends.
	Chunk string `json:"chunk,omitempty"`
	// Hits is the structured retrieval payload for Kind=="tool_retrieve"
	// events. Mirrors agent.RetrievalHit so the JSON shape matches.
	Hits []RetrievalHit `json:"hits,omitempty"`
}

// RetrievalHit mirrors agent.RetrievalHit for the Wails binding so the
// frontend type lives at the app boundary rather than reaching into the
// agent package.
type RetrievalHit struct {
	Path    string  `json:"path"`
	Doc     string  `json:"doc,omitempty"`
	Score   float32 `json:"score"`
	Snippet string  `json:"snippet,omitempty"`
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
// runnerType is the slash-command runner identifier ("react", "plan",
// "workflow", …) the frontend has picked for this turn. Empty string
// defaults to ReAct — that's what the legacy role picker passes when no
// slash command was used. See `agent.LookupRunner` for resolution
// semantics and TODO §9.1 for the registry shape.
func (a *App) StreamAgent(id, provider, system string, messages []ChatMessage, opts ChatOptions, toolNames []string, autoApprove []string, attachments []AttachmentInput, runnerType string) error {
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
		tools = append(tools, ctor(a))
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

		// Route through the runner registry. The slash-command parser
		// (TODO §9.2) supplies `runnerType`; an empty string defaults
		// to ReAct inside agent.LookupRunner so the legacy role-picker
		// path (which doesn't pass a runner type) continues to work.
		err := agent.RunByType(ctx, runnerType, c, req, tools, input, parts, gate, func(ev agent.StepEvent) {
			switch ev.Kind {
			case agent.StepText:
				runtime.EventsEmit(a.ctx, base+":chunk", AgentStepEvent{Kind: "text", Text: ev.Text})
			case agent.StepToolCall:
				runtime.EventsEmit(a.ctx, base+":chunk", AgentStepEvent{
					Kind: "tool_call", Name: ev.Name, Arguments: ev.Arguments,
				})
			case agent.StepToolStream:
				runtime.EventsEmit(a.ctx, base+":chunk", AgentStepEvent{
					Kind: "tool_stream", Name: ev.Name, Chunk: ev.Text,
				})
			case agent.StepRetrieve:
				hits := make([]RetrievalHit, len(ev.Hits))
				for i, h := range ev.Hits {
					hits[i] = RetrievalHit{Path: h.Path, Doc: h.Doc, Score: h.Score, Snippet: h.Snippet}
				}
				runtime.EventsEmit(a.ctx, base+":chunk", AgentStepEvent{
					Kind: "tool_retrieve", Name: ev.Name, Hits: hits,
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
