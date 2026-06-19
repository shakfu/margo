package core

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/shakfu/margo/pkg/margo/rag"
)

// WorkspaceRegistry owns the per-workspace RAG indexers and tracks which
// workspace the user currently has in focus. The front-end pushes the
// active id via SetActive whenever the user switches; the
// search_knowledge tool reads it via ActiveIndexer at invoke time.
type WorkspaceRegistry struct {
	openAIKey string

	mu       sync.Mutex
	active   string
	indexers map[string]*rag.Indexer
}

// NewWorkspaceRegistry constructs a registry. openAIKey is required to
// build embedders; pass "" and the registry will return nil from
// IndexerFor (callers must handle nil).
func NewWorkspaceRegistry(openAIKey string) *WorkspaceRegistry {
	return &WorkspaceRegistry{
		openAIKey: openAIKey,
		indexers:  map[string]*rag.Indexer{},
	}
}

// SetActive records the workspace currently in focus. No-op for unchanged ids.
func (r *WorkspaceRegistry) SetActive(id string) {
	r.mu.Lock()
	r.active = id
	r.mu.Unlock()
}

// IndexerFor returns the workspace's Indexer, creating it on first call.
// Returns nil when no OpenAI key is configured (the embedder cannot run)
// or workspaceID is empty; callers must handle nil.
func (r *WorkspaceRegistry) IndexerFor(workspaceID string) *rag.Indexer {
	if workspaceID == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if idx, ok := r.indexers[workspaceID]; ok {
		return idx
	}
	if r.openAIKey == "" {
		return nil
	}
	dir, err := rag.WorkspaceVectorDir(workspaceID)
	if err != nil {
		return nil
	}
	emb := rag.NewOpenAIEmbedder(r.openAIKey)
	store, err := rag.NewChromemStore(dir, workspaceID, emb.Dimensions())
	if err != nil {
		return nil
	}
	idx, err := rag.NewIndexer(emb, store, rag.IndexerOptions{
		SourcesPath: filepath.Join(dir, "sources.json"),
	})
	if err != nil {
		return nil
	}
	r.indexers[workspaceID] = idx
	return idx
}

// ActiveIndexer returns the indexer for the currently active workspace.
// Hot path: called every search_knowledge invocation. nil = no workspace
// or no embedder.
func (r *WorkspaceRegistry) ActiveIndexer() *rag.Indexer {
	r.mu.Lock()
	id := r.active
	r.mu.Unlock()
	return r.IndexerFor(id)
}

// IndexPath indexes a file or directory into the given workspace and
// returns the per-source counters.
func (r *WorkspaceRegistry) IndexPath(ctx context.Context, workspaceID, path string) (IndexResult, error) {
	if workspaceID == "" {
		return IndexResult{}, fmt.Errorf("workspace id is required")
	}
	idx := r.IndexerFor(workspaceID)
	if idx == nil {
		return IndexResult{}, fmt.Errorf("knowledge indexing requires OPENAI_API_KEY (used for embeddings)")
	}
	res, err := idx.IndexPath(ctx, path)
	if err != nil {
		return IndexResult{}, err
	}
	return IndexResult{Path: res.Path, FileCount: res.FileCount, ChunkCount: res.ChunkCount}, nil
}

// Sources lists what's currently indexed in the given workspace. Returns
// an empty slice (not nil) so JSON serialization yields `[]`.
func (r *WorkspaceRegistry) Sources(workspaceID string) []KnowledgeSource {
	out := []KnowledgeSource{}
	if workspaceID == "" {
		return out
	}
	idx := r.IndexerFor(workspaceID)
	if idx == nil {
		return out
	}
	for _, s := range idx.Sources() {
		out = append(out, KnowledgeSource{
			Path:       s.Path,
			IsDir:      s.IsDir,
			FileCount:  s.FileCount,
			ChunkCount: s.ChunkCount,
			IndexedAt:  s.IndexedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

// DeleteSource drops every chunk belonging to a source path.
func (r *WorkspaceRegistry) DeleteSource(ctx context.Context, workspaceID, path string) error {
	if workspaceID == "" {
		return fmt.Errorf("workspace id is required")
	}
	idx := r.IndexerFor(workspaceID)
	if idx == nil {
		return fmt.Errorf("no knowledge index for workspace %q", workspaceID)
	}
	return idx.DeleteSource(ctx, path)
}
