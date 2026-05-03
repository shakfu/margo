package margo

import "context"

// Role identifies the speaker of a Message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolDef declares a tool the model may call. Parameters is a JSON Schema
// object describing the tool's argument shape; nil means the tool takes no
// arguments. Providers convert this into their native tool format.
type ToolDef struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// ToolCall is a single tool invocation the model wants performed.
//
// On an assistant Message, ToolCalls carries one or more of these. To respond,
// the caller adds a Message with Role=RoleTool and ToolCallID set to the call's
// ID; Content is the tool's textual output.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string // JSON-encoded
}

// Message is one turn in a conversation. System prompts go in Request.System,
// not here, because providers (e.g. Anthropic) handle them as a separate field.
//
// Tool fields:
//   - On an assistant message, ToolCalls lists tool invocations the model wants.
//   - On a tool message, ToolCallID identifies which prior call this responds to;
//     Content carries the tool's result text.
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
}

// Thinking configures extended-thinking / reasoning where the provider
// supports it (Anthropic Claude 3.7+ and 4.x today). When Enabled is false the
// field is ignored. BudgetTokens is the maximum tokens the model may spend on
// internal reasoning before producing the final answer.
type Thinking struct {
	Enabled      bool
	BudgetTokens int
}

type Request struct {
	Model         string
	System        string
	Messages      []Message
	MaxTokens     int
	Temperature   *float64
	TopP          *float64
	StopSequences []string
	Thinking      *Thinking

	// Tools available to the model. When non-empty, the assistant may emit
	// ToolCalls in its response instead of (or alongside) Content.
	Tools []ToolDef

	// ToolChoice constrains tool selection: "" (default — provider's default,
	// usually "auto"), "auto", "none", "required", or a specific tool name.
	// Provider support varies; see provider docs.
	ToolChoice string
}

// Usage carries token counts and timing for a completion. Counts are populated
// when the provider reports them; FirstTokenMs/TotalMs are populated by the
// streaming path only (zero for non-stream Complete calls).
type Usage struct {
	InputTokens  int
	OutputTokens int
	FirstTokenMs int64
	TotalMs      int64
}

type Response struct {
	Text      string
	Thinking  string
	Model     string
	ToolCalls []ToolCall
	Usage     Usage
}

// ChunkKind classifies the payload of a streaming Chunk.
type ChunkKind string

const (
	ChunkText     ChunkKind = "text"
	ChunkThinking ChunkKind = "thinking"
	ChunkToolCall ChunkKind = "tool_call"
)

// Chunk is one streamed delta. The stream is closed when the channel closes;
// callers should also check Err on each chunk for mid-stream errors. The final
// chunk before the stream closes may carry Usage with no Text.
//
// When Kind == ChunkToolCall, ToolCall is populated with the (fully assembled)
// tool invocation and Text is empty. Providers accumulate streaming tool-call
// deltas internally and emit one ChunkToolCall per completed tool call before
// the final Usage chunk.
type Chunk struct {
	Kind     ChunkKind
	Text     string
	ToolCall *ToolCall
	Usage    *Usage
	Err      error
}

// Client is the provider-agnostic interface. Implementations live under
// pkg/margo/providers.
type Client interface {
	Name() string
	Complete(ctx context.Context, req Request) (Response, error)
	Stream(ctx context.Context, req Request) (<-chan Chunk, error)
}
