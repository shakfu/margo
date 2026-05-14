package rag

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// SourceInfo is a top-level record of one path the user explicitly indexed.
// A directory source's chunks span every file walked under it; a file source
// produces chunks from that one file. Tracking sources lets the UI present a
// "what's indexed" list independent of the vector store's internal structure
// and lets DeleteSource clean up by source rather than by chunk id.
type SourceInfo struct {
	// Path is the absolute, cleaned path the caller passed to IndexPath.
	// Used as the source's stable id (one source per distinct absolute
	// path within a collection).
	Path string `json:"path"`
	// IsDir reports whether the source was indexed as a directory walk.
	// Affects re-index semantics (a directory may gain or lose files).
	IsDir bool `json:"isDir"`
	// ChunkIDs is the complete flattened list of vector-store ids written
	// for this source. Held on disk so DeleteSource can issue a precise
	// store.Delete without having to scan the collection. Equal to the
	// concatenation of Files[*].ChunkIDs and kept in sync on every write.
	ChunkIDs []string `json:"chunkIds"`
	// Files records per-file mtime + chunk ids so re-indexing can skip
	// embedding work for files whose mtime hasn't changed since the last
	// run. Empty on sidecars written before this field existed; in that
	// case the first re-index after upgrade still re-embeds everything,
	// which is the conservative behaviour.
	Files []IndexedFile `json:"files,omitempty"`
	// FileCount and ChunkCount are convenience aggregates surfaced to the
	// UI; recomputable from ChunkIDs + Files but stored to avoid
	// re-parsing on every list call.
	FileCount  int `json:"fileCount"`
	ChunkCount int `json:"chunkCount"`
	// IndexedAt is when this source was last (re)indexed. Surfaced to the
	// UI so users can tell when a path is stale relative to disk.
	IndexedAt time.Time `json:"indexedAt"`
}

// IndexedFile is one file within a source's index. RelPath is stable across
// re-index calls (it's the path relative to the source root); ModTime is the
// file's mtime at the time of indexing; ChunkIDs are the vector-store ids
// produced for this file. On re-index, identical mtime allows the indexer to
// skip re-embedding this file's chunks.
type IndexedFile struct {
	RelPath  string    `json:"relPath"`
	ModTime  time.Time `json:"modTime"`
	ChunkIDs []string  `json:"chunkIds"`
}

// IndexResult summarises a single IndexPath call.
//
// FileCount is the total number of files the source resolves to (the walk
// result for directories, 1 for a file source). SkippedFiles is the subset
// of those whose mtime was unchanged since the last index run — their
// chunks were reused without re-embedding. EmbeddedFiles is the count
// that did require fresh embedding (FileCount - SkippedFiles, modulo
// files dropped due to errors).
type IndexResult struct {
	Path          string `json:"path"`
	FileCount     int    `json:"fileCount"`
	ChunkCount    int    `json:"chunkCount"`
	SkippedFiles  int    `json:"skippedFiles,omitempty"`
	EmbeddedFiles int    `json:"embeddedFiles,omitempty"`
}

// Indexer composes the loader, chunker, embedder, and vector store behind a
// single "index this path" / "search the index" surface. It also persists a
// sidecar `sources.json` next to the chromem .gob so the UI can list which
// paths have been indexed without scanning the vector collection.
//
// Concurrency: Indexer serialises mutating operations through `mu` so
// concurrent IndexPath / DeleteSource calls are well-defined; reads (Search,
// Sources) take the read lock briefly to snapshot.
type Indexer struct {
	embedder Embedder
	store    VectorStore
	chunker  RecursiveChunker

	mu          sync.RWMutex
	sources     map[string]*SourceInfo // keyed by absolute path
	sidecarPath string                 // empty = no persistence (in-memory tests)
}

// IndexerOptions configures an Indexer at construction. Zero values pick
// sensible defaults.
type IndexerOptions struct {
	// Chunker overrides the default recursive chunker. Zero value =
	// DefaultChunker().
	Chunker RecursiveChunker
	// SourcesPath is where to persist the sources sidecar. Empty means
	// no sidecar (in-memory only; useful for tests).
	SourcesPath string
}

// NewIndexer wires the four components together. The embedder and store
// must agree on dimensionality (an Upsert would otherwise fail at write).
func NewIndexer(emb Embedder, store VectorStore, opts IndexerOptions) (*Indexer, error) {
	if emb == nil {
		return nil, errors.New("indexer: embedder is required")
	}
	if store == nil {
		return nil, errors.New("indexer: store is required")
	}
	if emb.Dimensions() != store.Dimensions() {
		return nil, fmt.Errorf("indexer: embedder dim %d != store dim %d", emb.Dimensions(), store.Dimensions())
	}
	chunker := opts.Chunker
	if chunker.MaxChars == 0 {
		chunker = DefaultChunker()
	}
	idx := &Indexer{
		embedder:    emb,
		store:       store,
		chunker:     chunker,
		sources:     map[string]*SourceInfo{},
		sidecarPath: opts.SourcesPath,
	}
	if err := idx.loadSidecar(); err != nil {
		return nil, fmt.Errorf("indexer: load sidecar: %w", err)
	}
	return idx, nil
}

// IndexPath indexes a file or directory. Directories are walked using the
// loader's default extension allowlist (markdown / plain text). Re-indexing
// the same path replaces its prior chunks: stale ids from the prior run are
// deleted from the store before the new chunks are upserted.
func (idx *Indexer) IndexPath(ctx context.Context, path string) (IndexResult, error) {
	if path == "" {
		return IndexResult{}, errors.New("index: path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return IndexResult{}, fmt.Errorf("index: abs %q: %w", path, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return IndexResult{}, fmt.Errorf("index: stat %q: %w", abs, err)
	}

	var docs []LoadedDocument
	isDir := info.IsDir()
	if isDir {
		docs, err = Load(ctx, abs, LoadOptions{})
		if err != nil {
			return IndexResult{}, err
		}
	} else {
		// Single-file: hand-build a LoadedDocument rather than walking. The
		// loader's allowlist is intentionally bypassed: the user explicitly
		// asked for *this* file, so honour it as long as it's a readable
		// text file under the size cap.
		if info.Size() > DefaultMaxFileBytes {
			return IndexResult{}, fmt.Errorf("index: file %q exceeds %d bytes", abs, DefaultMaxFileBytes)
		}
		content, rerr := os.ReadFile(abs)
		if rerr != nil {
			return IndexResult{}, fmt.Errorf("index: read %q: %w", abs, rerr)
		}
		docs = []LoadedDocument{{
			AbsPath: abs,
			RelPath: filepath.Base(abs),
			Content: string(content),
			ModTime: info.ModTime(),
			Bytes:   info.Size(),
		}}
	}

	// Snapshot the prior per-file index so we can decide which files are
	// unchanged (mtime match → reuse chunks) vs. modified or added.
	priorFiles := map[string]IndexedFile{}
	idx.mu.RLock()
	if prior := idx.sources[abs]; prior != nil {
		for _, f := range prior.Files {
			priorFiles[f.RelPath] = f
		}
	}
	idx.mu.RUnlock()

	if len(docs) == 0 {
		// Replace any prior index for this source with an empty one so
		// the UI reflects the new state rather than silently keeping
		// stale chunks.
		if err := idx.replaceSource(ctx, abs, isDir, nil, nil, 0, 0); err != nil {
			return IndexResult{}, err
		}
		return IndexResult{Path: abs}, nil
	}

	// Walk the fresh load and split into "reuse" (mtime unchanged) and
	// "re-embed" (new or modified). Ids encode (source-hash, doc-relpath-
	// hash, chunk-index) so re-embedded chunks land at the same ids as
	// before — Upsert is replace-by-id.
	sourceID := hashID(abs)
	type chunkRow struct {
		id      string
		content string
		docRel  string
		index   int
	}
	var freshRows []chunkRow
	keptFiles := make([]IndexedFile, 0, len(docs))
	skippedCount := 0

	for _, d := range docs {
		if prior, ok := priorFiles[d.RelPath]; ok && prior.ModTime.Equal(d.ModTime) && len(prior.ChunkIDs) > 0 {
			// Unchanged: keep the existing ChunkIDs as-is; they already
			// live in the vector store.
			keptFiles = append(keptFiles, prior)
			skippedCount++
			continue
		}
		pieces := idx.chunker.Split(d.Content)
		if len(pieces) == 0 {
			// File was readable but produced no chunks (empty content).
			// Still record it so a future re-index sees it as known;
			// just with an empty chunk list.
			keptFiles = append(keptFiles, IndexedFile{
				RelPath: d.RelPath,
				ModTime: d.ModTime,
			})
			continue
		}
		fileChunkIDs := make([]string, len(pieces))
		for i, p := range pieces {
			id := chunkID(sourceID, d.RelPath, i)
			freshRows = append(freshRows, chunkRow{
				id:      id,
				content: p,
				docRel:  d.RelPath,
				index:   i,
			})
			fileChunkIDs[i] = id
		}
		keptFiles = append(keptFiles, IndexedFile{
			RelPath:  d.RelPath,
			ModTime:  d.ModTime,
			ChunkIDs: fileChunkIDs,
		})
		if err := ctx.Err(); err != nil {
			return IndexResult{}, err
		}
	}

	// Embed only the fresh rows. Hot path: a re-index with zero changes
	// makes no embedder calls.
	if len(freshRows) > 0 {
		texts := make([]string, len(freshRows))
		for i, r := range freshRows {
			texts[i] = r.content
		}
		vecs, err := idx.embedder.EmbedBatch(ctx, texts)
		if err != nil {
			return IndexResult{}, fmt.Errorf("index: embed: %w", err)
		}
		if len(vecs) != len(freshRows) {
			return IndexResult{}, fmt.Errorf("index: embedder returned %d vectors for %d chunks", len(vecs), len(freshRows))
		}
		docsOut := make([]Document, len(freshRows))
		for i, r := range freshRows {
			docsOut[i] = Document{
				ID:      r.id,
				Vector:  vecs[i],
				Content: r.content,
				Metadata: map[string]string{
					"source":     abs,
					"doc":        r.docRel,
					"chunkIndex": fmt.Sprintf("%d", r.index),
				},
			}
		}
		if err := idx.store.Upsert(ctx, docsOut); err != nil {
			return IndexResult{}, fmt.Errorf("index: upsert: %w", err)
		}
	}

	// Recompute the flat ChunkIDs list and replace the source record.
	// replaceSource handles pruning of any ids that belonged to files
	// dropped from this source (deleted-on-disk files in a dir source,
	// or just files whose chunk count shrank after re-embed).
	flat := make([]string, 0, len(keptFiles)*2)
	for _, f := range keptFiles {
		flat = append(flat, f.ChunkIDs...)
	}
	if err := idx.replaceSource(ctx, abs, isDir, keptFiles, flat, len(docs), len(flat)); err != nil {
		return IndexResult{}, err
	}
	return IndexResult{
		Path:          abs,
		FileCount:     len(docs),
		ChunkCount:    len(flat),
		SkippedFiles:  skippedCount,
		EmbeddedFiles: len(docs) - skippedCount,
	}, nil
}

// Search embeds the query and returns the top-k matches across the entire
// collection. Empty store returns nil, nil.
func (idx *Indexer) Search(ctx context.Context, query string, k int) ([]QueryResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("search: query is required")
	}
	if k <= 0 {
		k = 5
	}
	vec, err := idx.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("search: embed: %w", err)
	}
	return idx.store.Query(ctx, vec, k, nil)
}

// Sources returns a snapshot of currently indexed sources, sorted by Path.
func (idx *Indexer) Sources() []SourceInfo {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	out := make([]SourceInfo, 0, len(idx.sources))
	for _, s := range idx.sources {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// DeleteSource removes every chunk associated with the given source path
// from the vector store and drops the source from the sidecar.
func (idx *Indexer) DeleteSource(ctx context.Context, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("delete: abs %q: %w", path, err)
	}
	idx.mu.Lock()
	src, ok := idx.sources[abs]
	if !ok {
		idx.mu.Unlock()
		return fmt.Errorf("delete: no source indexed at %q", abs)
	}
	ids := append([]string(nil), src.ChunkIDs...)
	delete(idx.sources, abs)
	persistErr := idx.persistSidecarLocked()
	idx.mu.Unlock()

	if err := idx.store.Delete(ctx, ids); err != nil {
		return fmt.Errorf("delete: store: %w", err)
	}
	return persistErr
}

// Close releases the underlying store. Idempotent.
func (idx *Indexer) Close() error {
	if idx.store == nil {
		return nil
	}
	return idx.store.Close()
}

// replaceSource is the atomic-from-the-sidecar's-perspective swap of one
// source's prior chunk-id set with a new one. Stale ids (in old set but
// not new) are removed from the store before the new sidecar lands.
func (idx *Indexer) replaceSource(ctx context.Context, abs string, isDir bool, files []IndexedFile, newIDs []string, fileCount, chunkCount int) error {
	idx.mu.Lock()
	prior := idx.sources[abs]
	var stale []string
	if prior != nil {
		newSet := make(map[string]struct{}, len(newIDs))
		for _, id := range newIDs {
			newSet[id] = struct{}{}
		}
		for _, id := range prior.ChunkIDs {
			if _, keep := newSet[id]; !keep {
				stale = append(stale, id)
			}
		}
	}
	idx.sources[abs] = &SourceInfo{
		Path:       abs,
		IsDir:      isDir,
		ChunkIDs:   append([]string(nil), newIDs...),
		Files:      append([]IndexedFile(nil), files...),
		FileCount:  fileCount,
		ChunkCount: chunkCount,
		IndexedAt:  time.Now(),
	}
	persistErr := idx.persistSidecarLocked()
	idx.mu.Unlock()

	if len(stale) > 0 {
		if err := idx.store.Delete(ctx, stale); err != nil {
			return fmt.Errorf("replaceSource: delete stale: %w", err)
		}
	}
	return persistErr
}

func (idx *Indexer) persistSidecarLocked() error {
	if idx.sidecarPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(idx.sidecarPath), 0o755); err != nil {
		return fmt.Errorf("persist sidecar: mkdir: %w", err)
	}
	out := make([]SourceInfo, 0, len(idx.sources))
	for _, s := range idx.sources {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	buf, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("persist sidecar: marshal: %w", err)
	}
	tmp := idx.sidecarPath + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o644); err != nil {
		return fmt.Errorf("persist sidecar: write: %w", err)
	}
	if err := os.Rename(tmp, idx.sidecarPath); err != nil {
		return fmt.Errorf("persist sidecar: rename: %w", err)
	}
	return nil
}

func (idx *Indexer) loadSidecar() error {
	if idx.sidecarPath == "" {
		return nil
	}
	buf, err := os.ReadFile(idx.sidecarPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var entries []SourceInfo
	if err := json.Unmarshal(buf, &entries); err != nil {
		return fmt.Errorf("parse sidecar: %w", err)
	}
	for i := range entries {
		e := entries[i]
		idx.sources[e.Path] = &e
	}
	return nil
}

func hashID(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

func chunkID(sourceID, docRel string, chunkIdx int) string {
	sum := sha256.Sum256([]byte(docRel))
	return fmt.Sprintf("%s-%s-%d", sourceID, hex.EncodeToString(sum[:6]), chunkIdx)
}
