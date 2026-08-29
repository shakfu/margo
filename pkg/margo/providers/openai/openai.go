// Package openai implements margo.Client against OpenAI's Chat
// Completions API.
//
// The wire-format work lives in providers/openaicompat, which OpenRouter
// shares. This package is the OpenAI-specific configuration: endpoint
// default, model default, and the provider name core routes on.
package openai

import (
	"strings"

	"github.com/shakfu/margo/pkg/margo/providers/openaicompat"
)

// defaultModel is used when a Request leaves Model empty. Cheapest
// current-generation entry, so an unconfigured call is not an expensive
// surprise.
const defaultModel = "gpt-5.4-nano"

// Client is an OpenAI-configured openaicompat.Client.
type Client = openaicompat.Client

// nonChatMarkers appear in the ids of OpenAI models that /v1/models
// returns but that cannot serve a chat completion — embeddings, speech,
// images, moderation. The endpoint offers no "modality" field to filter
// on, so substring matching is what is available.
var nonChatMarkers = []string{
	"embedding", "whisper", "tts", "dall-e", "moderation",
	"audio", "realtime", "transcribe", "image", "search", "codex",
}

// isChatModel decides whether an id from /v1/models belongs in a model
// picker. Deliberately an allowlist of prefixes plus a denylist of
// markers: a new chat family shows up only after someone adds its
// prefix, which is the safer direction to be wrong in — a missing model
// is a one-line fix, whereas a picker full of `text-embedding-3-small`
// is a bug report.
func isChatModel(id string) bool {
	lower := strings.ToLower(id)
	for _, m := range nonChatMarkers {
		if strings.Contains(lower, m) {
			return false
		}
	}
	for _, p := range []string{"gpt-", "chatgpt-", "o1", "o3", "o4"} {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// New returns a client for the OpenAI API.
func New(apiKey string) *Client {
	return openaicompat.New(openaicompat.Options{
		Name:         "openai",
		APIKey:       apiKey,
		DefaultModel: defaultModel,
		ModelFilter:  isChatModel,
	})
}
