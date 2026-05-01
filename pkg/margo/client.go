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

type Request struct {
	Model     string
	System    string
	Messages  []Message
	MaxTokens int
}

type Response struct {
	Text  string
	Model string
}

// Chunk is one streamed delta. The stream is closed when the channel closes;
// callers should also check Err on each chunk for mid-stream errors.
type Chunk struct {
	Text string
	Err  error
}

// Client is the provider-agnostic interface. Implementations live under
// pkg/margo/providers.
type Client interface {
	Name() string
	Complete(ctx context.Context, req Request) (Response, error)
	Stream(ctx context.Context, req Request) (<-chan Chunk, error)
}
