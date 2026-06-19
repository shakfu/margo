// Package core is margo's UI-agnostic library: it exposes a Session whose
// streaming methods return Go channels of Event values, so any front-end
// can consume the same orchestration code without depending on a specific
// transport. The shipped binaries (a desktop GUI, a terminal UI, a CLI) are
// interchangeable consumers; none of them is privileged by this package.
//
// The package owns:
//   - provider clients + model catalog
//   - request/response and event types (carry JSON tags so a transport
//     layer can pass them through unchanged, but core itself never serializes)
//   - workspace + RAG indexer registry
//   - attachment store (bytes in, bytes out — transport encoding such as
//     base64 is a front-end concern)
//   - permission broker (channel-based, no UI assumptions)
//   - tool registry
//
// What it does NOT do: OS dialogs, file pickers, event emission, opening
// URLs/files. Those belong to the consumer binary.
package core

// Role identifies the speaker of a Message in a conversation history.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

// Message is a single turn in a conversation history fed to a provider.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Attachment is a binary payload accompanying the most recent user-role
// message (image, PDF, …). Data holds the raw bytes; the front-end is
// responsible for any transport encoding (base64, multipart).
type Attachment struct {
	Name     string
	MimeType string
	Data     []byte
}

// Options carries per-request sampling and reasoning settings.
type Options struct {
	Model         string   `json:"model"`
	MaxTokens     int      `json:"maxTokens"`
	Temperature   *float64 `json:"temperature"`
	TopP          *float64 `json:"topP"`
	StopSequences []string `json:"stopSequences"`
	ThinkEnabled  bool     `json:"thinkEnabled"`
	ThinkBudget   int      `json:"thinkBudget"`
}

// Usage is the timing/token report associated with a completed run.
type Usage struct {
	InputTokens  int   `json:"inputTokens"`
	OutputTokens int   `json:"outputTokens"`
	FirstTokenMs int64 `json:"firstTokenMs"`
	TotalMs      int64 `json:"totalMs"`
}

// Response is the non-streaming completion result.
type Response struct {
	Text     string `json:"text"`
	Thinking string `json:"thinking"`
	Model    string `json:"model"`
	Usage    Usage  `json:"usage"`
}

// EventKind discriminates Event variants emitted by Session.Stream and
// Session.StreamAgent. Plain chat runs only emit Text / Thinking / Done /
// Error. Agent runs also emit ToolCall / ToolStream / ToolRetrieve /
// ToolResult / Permission.
type EventKind string

const (
	EventText         EventKind = "text"
	EventThinking     EventKind = "thinking"
	EventToolCall     EventKind = "tool_call"
	EventToolStream   EventKind = "tool_stream"
	EventToolRetrieve EventKind = "tool_retrieve"
	EventToolResult   EventKind = "tool_result"
	EventPermission   EventKind = "permission"
	EventDone         EventKind = "done"
	EventError        EventKind = "error"
)

// Event is the unified streaming event. The active fields depend on Kind;
// consumers switch on Kind and read the relevant fields.
type Event struct {
	Kind EventKind

	// Text content (Text / Thinking events) or trailing text on a tool
	// stream chunk. Also carries the error message for Error events
	// when Err is nil (provider sometimes signals via text).
	Text string

	// Tool-call identity for ToolCall / ToolStream / ToolResult /
	// ToolRetrieve / Permission events.
	Name      string
	Arguments string

	// ToolResult fields.
	Result  string
	IsError bool

	// ToolStream chunk content.
	Chunk string

	// ToolRetrieve structured hits.
	Hits []RetrievalHit

	// PermissionID is the broker id the front-end echoes back via
	// Session.RespondPermission. Set only on Permission events.
	PermissionID string

	// Usage is set on Done events when the provider reported usage.
	Usage *Usage

	// Err carries a structured error on EventError. When nil but Kind
	// is EventError, Text holds the error message instead.
	Err error
}

// RetrievalHit is a single hit returned by a RAG search tool, surfaced to
// the front-end so it can render a citation card.
type RetrievalHit struct {
	Path    string  `json:"path"`
	Doc     string  `json:"doc,omitempty"`
	Score   float32 `json:"score"`
	Snippet string  `json:"snippet,omitempty"`
}

// StoredAttachment is the on-disk record returned by AttachmentStore.Save.
type StoredAttachment struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	Size     int64  `json:"size"`
}

// IndexResult is the per-source counter returned after IndexPath.
type IndexResult struct {
	Path       string `json:"path"`
	FileCount  int    `json:"fileCount"`
	ChunkCount int    `json:"chunkCount"`
}

// KnowledgeSource describes one indexed source in a workspace.
type KnowledgeSource struct {
	Path       string `json:"path"`
	IsDir      bool   `json:"isDir"`
	FileCount  int    `json:"fileCount"`
	ChunkCount int    `json:"chunkCount"`
	IndexedAt  string `json:"indexedAt"`
}

// ToolMetadata is the descriptive payload for the agent-mode tool catalog.
type ToolMetadata struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	IsReadOnly   bool   `json:"isReadOnly"`
	IsStreamable bool   `json:"isStreamable"`
}

// ChatRequest is the input to Session.Chat / Session.Stream.
type ChatRequest struct {
	Provider    string
	System      string
	Messages    []Message
	Options     Options
	Attachments []Attachment
}

// AgentRequest is the input to Session.StreamAgent.
type AgentRequest struct {
	ChatRequest

	// ToolNames selects which tools from the registry the agent may
	// call. Empty = plain chat (prefer Session.Stream in that case).
	ToolNames []string

	// AutoApprove lists tool names the user has pre-authorized for
	// this run. Augmented when the user clicks "Always" on a live
	// permission prompt.
	AutoApprove []string

	// RunnerType selects the slash-command runner ("react", "plan",
	// "workflow"). Empty defaults to ReAct.
	RunnerType string
}
