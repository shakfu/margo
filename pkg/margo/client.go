package margo

import "context"

// Client is the provider-agnostic interface implemented by each backend
// (Anthropic, OpenAI, ...). Implementations live under pkg/margo/providers.
type Client interface {
	// Name returns the provider identifier (e.g. "anthropic", "openai").
	Name() string

	// Complete sends a single-turn prompt and returns the model's text response.
	Complete(ctx context.Context, req Request) (Response, error)
}

type Request struct {
	Model     string
	Prompt    string
	MaxTokens int
}

type Response struct {
	Text  string
	Model string
}
