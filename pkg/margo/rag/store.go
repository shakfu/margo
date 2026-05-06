package rag

import "context"

// Document is a single indexed item: an opaque ID, a pre-computed
// embedding vector (the store does not embed for you), the original
// content (kept verbatim for retrieval display), and optional
// metadata for tag-style filtering.
//
// IDs are caller-chosen and must be unique within a store. Re-upserting
// the same ID replaces the prior version atomically.
type Document struct {
	ID       string
	Vector   []float32
	Content  string
	Metadata map[string]string
}

// QueryResult is one match returned by VectorStore.Query.
//
// Score is cosine similarity in [-1, 1]; higher = more similar. Stores
// must return results sorted by Score descending so callers can take
// the top-k without re-sorting.
type QueryResult struct {
	Document
	Score float32
}

// VectorStore persists Documents keyed by ID and supports k-nearest-
// neighbour query over their vectors. Implementations must be safe
// for concurrent use.
//
// Storage and embedding are deliberately separated: callers compute
// embeddings via an Embedder (or any other source) and hand pre-built
// vectors to Upsert. The store does no embedding of its own, which
// keeps the abstraction usable for non-OpenAI embedders, hand-built
// vectors, dimensionality-reduced inputs, etc.
//
// Persistence semantics are implementation-defined; backends that
// auto-flush after writes treat Persist as a no-op. Callers wanting an
// explicit fsync point should call Persist before exiting.
type VectorStore interface {
	// Name returns a stable identifier of the form "<backend>:<scope>"
	// for telemetry / debug logging.
	Name() string
	// Dimensions reports the expected vector size; mismatched Upserts
	// or Queries return an error rather than silently truncating.
	Dimensions() int
	// Count returns the number of documents currently stored.
	Count() int

	// Upsert inserts new documents or replaces existing ones with
	// matching IDs. All documents are written or none are; on
	// error the store is unchanged.
	Upsert(ctx context.Context, docs []Document) error
	// Query returns up to k results most similar to vec. `where`
	// is an optional exact-match metadata filter (intersected; nil
	// means "no filter"). Returns nil when the store is empty.
	Query(ctx context.Context, vec []float32, k int, where map[string]string) ([]QueryResult, error)
	// Delete removes the specified IDs. Missing IDs are silently
	// skipped (idempotent semantics, matching chromem-go).
	Delete(ctx context.Context, ids []string) error

	// Persist forces a flush to durable storage. Implementations
	// that auto-flush may treat this as a no-op.
	Persist(ctx context.Context) error
	// Close releases resources. Should also Persist if dirty.
	Close() error
}
