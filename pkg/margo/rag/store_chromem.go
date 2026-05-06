package rag

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	chromem "github.com/philippgille/chromem-go"
)

// collectionName is the chromem-go collection a ChromemStore writes
// to. Per workspace we use one collection of "documents"; in chromem
// terms this becomes a single .gob file at
// <baseDir>/sn_documents.gob (chromem prefixes "sn_").
const chromemCollectionName = "documents"

// ChromemStore is a VectorStore backed by chromem-go.
// (TODO §6.6.A; REVIEW.md §7.1.d.2.)
//
// Pure Go, in-process, persisted as a .gob file under baseDir.
// chromem-go auto-flushes synchronously on every write, so Persist is
// a no-op on this backend; we still expose it on the interface so
// future backends (in-memory + manual export) can use it.
//
// Concurrency: chromem-go's Collection guards its internal map with a
// RWMutex, so concurrent Upsert/Query/Delete is safe.
type ChromemStore struct {
	db         *chromem.DB
	collection *chromem.Collection
	scope      string // for Name(); typically the workspace id
	dimensions int
	baseDir    string // empty = in-memory only
}

// stubEmbedFunc panics-loudly when chromem-go reaches for it. We
// always pass pre-computed vectors via Upsert/Query, so this code
// path should be unreachable. If it ever fires, it means a future
// caller used Collection.Query (text-based) — wrong API for this
// backend.
func stubEmbedFunc(_ context.Context, _ string) ([]float32, error) {
	return nil, errors.New("rag.ChromemStore: text-based query not supported; use VectorStore.Query with a pre-computed vector")
}

// NewChromemStore opens (or creates) a vector store at baseDir. If
// baseDir is empty, the store is in-memory only — useful for tests.
//
// dimensions is the expected vector size; mismatched Upserts return
// an error. scope is a free-form identifier (typically a workspace
// id) used in Name() for diagnostics.
func NewChromemStore(baseDir, scope string, dimensions int) (*ChromemStore, error) {
	if dimensions <= 0 {
		return nil, fmt.Errorf("chromem: dimensions must be > 0, got %d", dimensions)
	}
	var db *chromem.DB
	if baseDir == "" {
		db = chromem.NewDB()
	} else {
		if err := os.MkdirAll(baseDir, 0o755); err != nil {
			return nil, fmt.Errorf("chromem: mkdir %q: %w", baseDir, err)
		}
		var err error
		db, err = chromem.NewPersistentDB(baseDir, false)
		if err != nil {
			return nil, fmt.Errorf("chromem: open persistent db at %q: %w", baseDir, err)
		}
	}
	coll, err := db.GetOrCreateCollection(chromemCollectionName, nil, stubEmbedFunc)
	if err != nil {
		return nil, fmt.Errorf("chromem: get/create collection: %w", err)
	}
	return &ChromemStore{
		db:         db,
		collection: coll,
		scope:      scope,
		dimensions: dimensions,
		baseDir:    baseDir,
	}, nil
}

func (s *ChromemStore) Name() string {
	if s.scope == "" {
		return "chromem"
	}
	return "chromem:" + s.scope
}

func (s *ChromemStore) Dimensions() int { return s.dimensions }

func (s *ChromemStore) Count() int { return s.collection.Count() }

func (s *ChromemStore) Upsert(ctx context.Context, docs []Document) error {
	if len(docs) == 0 {
		return nil
	}
	// Validate inputs before any side effects so a bad doc in the
	// middle of a batch doesn't leave the store half-updated.
	ids := make([]string, len(docs))
	chromemDocs := make([]chromem.Document, len(docs))
	for i, d := range docs {
		if d.ID == "" {
			return fmt.Errorf("upsert: doc %d has empty ID", i)
		}
		if len(d.Vector) != s.dimensions {
			return fmt.Errorf("upsert: doc %d (id=%q): %d-dim vector, want %d",
				i, d.ID, len(d.Vector), s.dimensions)
		}
		ids[i] = d.ID
		chromemDocs[i] = chromem.Document{
			ID:        d.ID,
			Embedding: d.Vector,
			Content:   d.Content,
			Metadata:  d.Metadata,
		}
	}
	// chromem-go's AddDocuments doesn't replace existing IDs — it
	// would silently double-store. Pre-delete to make Upsert
	// genuinely "insert or replace." Delete is idempotent for
	// missing IDs, so this is safe on first insertion too.
	if err := s.collection.Delete(ctx, nil, nil, ids...); err != nil {
		return fmt.Errorf("upsert: pre-delete: %w", err)
	}
	// Concurrency 1 = sequential; chromem-go does internal
	// synchronisation so higher values are safe but unnecessary
	// for our typical batch sizes (tens to a few hundred).
	if err := s.collection.AddDocuments(ctx, chromemDocs, 1); err != nil {
		return fmt.Errorf("upsert: add: %w", err)
	}
	return nil
}

func (s *ChromemStore) Query(ctx context.Context, vec []float32, k int, where map[string]string) ([]QueryResult, error) {
	if len(vec) != s.dimensions {
		return nil, fmt.Errorf("query: %d-dim vector, want %d", len(vec), s.dimensions)
	}
	if k <= 0 {
		return nil, nil
	}
	// chromem-go errors when nResults > collection.Count(); cap it.
	count := s.collection.Count()
	if count == 0 {
		return nil, nil
	}
	if k > count {
		k = count
	}
	results, err := s.collection.QueryEmbedding(ctx, vec, k, where, nil)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	out := make([]QueryResult, len(results))
	for i, r := range results {
		out[i] = QueryResult{
			Document: Document{
				ID:       r.ID,
				Vector:   r.Embedding,
				Content:  r.Content,
				Metadata: r.Metadata,
			},
			Score: r.Similarity,
		}
	}
	return out, nil
}

func (s *ChromemStore) Delete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if err := s.collection.Delete(ctx, nil, nil, ids...); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}

// Persist is a no-op on the chromem-go backend: NewPersistentDB
// auto-flushes synchronously on every write. Kept on the interface
// for backends that buffer (in-memory with manual export, future
// Qdrant client, etc.).
func (s *ChromemStore) Persist(_ context.Context) error { return nil }

// Close releases the in-memory state. With chromem-go's auto-flush
// model there is no buffered state to drain; Close exists for
// interface symmetry.
func (s *ChromemStore) Close() error {
	s.db = nil
	s.collection = nil
	return nil
}

// WorkspaceVectorDir returns the canonical on-disk directory for a
// workspace's vector index: $os.UserConfigDir/Margo/vectors/<id>.
// Caller is responsible for using it (NewChromemStore creates the
// dir if missing).
func WorkspaceVectorDir(workspaceID string) (string, error) {
	if workspaceID == "" {
		return "", errors.New("workspace id required")
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("user config dir: %w", err)
	}
	return filepath.Join(cfg, "Margo", "vectors", workspaceID), nil
}
