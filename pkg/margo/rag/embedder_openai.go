package rag

import (
	"context"
	"fmt"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
)

// Default OpenAI embedding model. text-embedding-3-small is the
// best price/quality point in the OpenAI line for English-heavy
// corpora as of 2026; output dim 1536. For larger / multilingual
// corpora switch to text-embedding-3-large (3072) via WithModel.
const (
	DefaultOpenAIEmbedModel = "text-embedding-3-small"
	DefaultOpenAIEmbedDims  = 1536
)

// OpenAIEmbedder is an Embedder backed by OpenAI's embeddings API.
// Concurrent-safe (the underlying SDK client is).
type OpenAIEmbedder struct {
	client     sdk.Client
	model      string
	dimensions int
}

// OpenAIEmbedderOption configures an OpenAIEmbedder.
type OpenAIEmbedderOption func(*OpenAIEmbedder)

// WithModel selects an embedding model id. Caller must also supply a
// matching WithDimensions if not using DefaultOpenAIEmbedDims.
func WithModel(model string) OpenAIEmbedderOption {
	return func(e *OpenAIEmbedder) { e.model = model }
}

// WithDimensions overrides the requested output vector size. Must be
// supported by the model (text-embedding-3-* allow truncation;
// text-embedding-ada-002 does not).
func WithDimensions(d int) OpenAIEmbedderOption {
	return func(e *OpenAIEmbedder) { e.dimensions = d }
}

// NewOpenAIEmbedder constructs an embedder authenticated with apiKey.
// Defaults to text-embedding-3-small at 1536 dims.
func NewOpenAIEmbedder(apiKey string, opts ...OpenAIEmbedderOption) *OpenAIEmbedder {
	e := &OpenAIEmbedder{
		client:     sdk.NewClient(option.WithAPIKey(apiKey)),
		model:      DefaultOpenAIEmbedModel,
		dimensions: DefaultOpenAIEmbedDims,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

func (e *OpenAIEmbedder) Name() string    { return "openai:" + e.model }
func (e *OpenAIEmbedder) Dimensions() int { return e.dimensions }

func (e *OpenAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, fmt.Errorf("embed: empty text")
	}
	out, err := e.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

func (e *OpenAIEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	for i, t := range texts {
		if t == "" {
			return nil, fmt.Errorf("embed: empty text at index %d", i)
		}
	}
	resp, err := e.client.Embeddings.New(ctx, sdk.EmbeddingNewParams{
		Input:      sdk.EmbeddingNewParamsInputUnion{OfArrayOfStrings: texts},
		Model:      e.model,
		Dimensions: param.NewOpt(int64(e.dimensions)),
	})
	if err != nil {
		return nil, fmt.Errorf("openai embed: %w", err)
	}
	if len(resp.Data) != len(texts) {
		return nil, fmt.Errorf("openai embed: got %d vectors for %d inputs", len(resp.Data), len(texts))
	}
	// The API documents that responses are returned in input order,
	// but the wire format carries an explicit Index. Trust it over
	// position to be safe against future SDK or upstream reorderings.
	out := make([][]float32, len(texts))
	for _, d := range resp.Data {
		idx := int(d.Index)
		if idx < 0 || idx >= len(texts) {
			return nil, fmt.Errorf("openai embed: out-of-range index %d (have %d inputs)", idx, len(texts))
		}
		v := make([]float32, len(d.Embedding))
		for j, x := range d.Embedding {
			v[j] = float32(x)
		}
		out[idx] = v
	}
	for i, v := range out {
		if v == nil {
			return nil, fmt.Errorf("openai embed: missing vector at index %d", i)
		}
	}
	return out, nil
}
