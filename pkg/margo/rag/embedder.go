// Package rag holds the retrieval-augmented-generation building
// blocks: an Embedder for turning text into vectors, a VectorStore
// for persisting and querying them, and (in later slices) loaders /
// chunkers / orchestrators that use both. See REVIEW.md §7.1.d.
//
// 7.1.d.1 ships only the Embedder and an OpenAI implementation;
// later sub-slices add the rest.
package rag

import "context"

// Embedder turns text into fixed-dimension float vectors. Implementations
// must be safe to call concurrently.
//
// Vectors are []float32 (rather than the []float64 the OpenAI SDK
// returns) because every downstream consumer in the RAG pipeline —
// chromem-go, faiss-style stores, and most cosine/dot-product helpers
// — uses float32. Converting at the boundary keeps the interface
// uniform.
type Embedder interface {
	// Name returns a stable identifier of the form "<provider>:<model>"
	// for telemetry / debug logging. Not parsed.
	Name() string
	// Dimensions reports the length of the output vectors. Constant
	// for a given Embedder instance; downstream stores key on this.
	Dimensions() int
	// Embed turns a single non-empty string into one vector.
	Embed(ctx context.Context, text string) ([]float32, error)
	// EmbedBatch turns N non-empty strings into N vectors, in the
	// same order as the input slice. Empty input returns nil, nil.
	// Implementations may batch under the hood; callers should prefer
	// this over a per-string loop for throughput.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}
