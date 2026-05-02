package margo

import "context"

// Role identifies the speaker of a Message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one turn in a conversation. System prompts go in Request.System,
// not here, because providers (e.g. Anthropic) handle them as a separate field.
type Message struct {
	Role    Role
	Content string
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
	Text     string
	Thinking string
	Model    string
	Usage    Usage
}

// ChunkKind classifies the payload of a streaming Chunk.
type ChunkKind string

const (
	ChunkText     ChunkKind = "text"
	ChunkThinking ChunkKind = "thinking"
)

// Chunk is one streamed delta. The stream is closed when the channel closes;
// callers should also check Err on each chunk for mid-stream errors. The final
// chunk before the stream closes may carry Usage with no Text.
type Chunk struct {
	Kind  ChunkKind
	Text  string
	Usage *Usage
	Err   error
}

// Client is the provider-agnostic interface. Implementations live under
// pkg/margo/providers.
type Client interface {
	Name() string
	Complete(ctx context.Context, req Request) (Response, error)
	Stream(ctx context.Context, req Request) (<-chan Chunk, error)
}
