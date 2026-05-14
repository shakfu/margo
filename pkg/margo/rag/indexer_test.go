package rag

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// stubEmbedder is a deterministic, no-network embedder for tests. Each text
// hashes to a 4-float vector by chunking sha256 into uint32s; identical text
// always yields the same vector, so the Search test below can match a query
// to its source chunk by equality.
type stubEmbedder struct {
	dim int
}

func (s *stubEmbedder) Name() string    { return "stub" }
func (s *stubEmbedder) Dimensions() int { return s.dim }

func (s *stubEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, errors.New("empty")
	}
	sum := sha256.Sum256([]byte(text))
	out := make([]float32, s.dim)
	for i := 0; i < s.dim; i++ {
		// 4 bytes of the hash per dim, cycled.
		off := (i * 4) % (len(sum) - 3)
		u := binary.BigEndian.Uint32(sum[off : off+4])
		out[i] = float32(u) / float32(math.MaxUint32)
	}
	return out, nil
}

func (s *stubEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v, err := s.Embed(ctx, t)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func newTestIndexer(t *testing.T) (*Indexer, string) {
	t.Helper()
	dir := t.TempDir()
	emb := &stubEmbedder{dim: 4}
	store, err := NewChromemStore(dir, "test", emb.Dimensions())
	if err != nil {
		t.Fatalf("NewChromemStore: %v", err)
	}
	idx, err := NewIndexer(emb, store, IndexerOptions{
		SourcesPath: filepath.Join(dir, "sources.json"),
	})
	if err != nil {
		t.Fatalf("NewIndexer: %v", err)
	}
	t.Cleanup(func() { idx.Close() })
	return idx, dir
}

func TestIndexerIndexAndSearchFile(t *testing.T) {
	idx, _ := newTestIndexer(t)

	f := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(f, []byte("Alpha bravo charlie. Delta echo foxtrot."), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	r, err := idx.IndexPath(context.Background(), f)
	if err != nil {
		t.Fatalf("IndexPath: %v", err)
	}
	if r.FileCount != 1 || r.ChunkCount < 1 {
		t.Fatalf("IndexResult: %+v", r)
	}

	srcs := idx.Sources()
	if len(srcs) != 1 || srcs[0].Path == "" || srcs[0].ChunkCount != r.ChunkCount {
		t.Fatalf("Sources: %+v", srcs)
	}

	// Query the exact indexed text — stub embedder is deterministic, so the
	// query vector equals the chunk vector and similarity is 1.0.
	results, err := idx.Search(context.Background(), "Alpha bravo charlie. Delta echo foxtrot.", 3)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected at least one result")
	}
	if results[0].Metadata["source"] == "" {
		t.Errorf("expected source metadata, got %+v", results[0].Metadata)
	}
}

func TestIndexerIndexDirectory(t *testing.T) {
	idx, _ := newTestIndexer(t)
	dir := t.TempDir()
	for i, name := range []string{"a.md", "b.txt", "c.qmd"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("content "+string(rune('a'+i))), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	// One ignored extension to confirm the loader allowlist gates the walk.
	if err := os.WriteFile(filepath.Join(dir, "skip.png"), []byte("binary"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	r, err := idx.IndexPath(context.Background(), dir)
	if err != nil {
		t.Fatalf("IndexPath: %v", err)
	}
	if r.FileCount != 3 {
		t.Errorf("FileCount: got %d, want 3 (.png should be skipped)", r.FileCount)
	}
}

func TestIndexerReindexReplaces(t *testing.T) {
	idx, _ := newTestIndexer(t)
	f := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(f, []byte("first version of the note"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	r1, _ := idx.IndexPath(context.Background(), f)
	count1 := idx.store.Count()
	if count1 != r1.ChunkCount {
		t.Fatalf("count mismatch after first index: store=%d, result=%d", count1, r1.ChunkCount)
	}

	// Replace the file with a much longer body so it produces a different
	// chunk count; the prior chunk ids that are no longer produced must be
	// pruned from the store.
	body := ""
	for i := 0; i < 200; i++ {
		body += "paragraph " + string(rune('A'+(i%26))) + " text. "
	}
	if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	r2, err := idx.IndexPath(context.Background(), f)
	if err != nil {
		t.Fatalf("re-index: %v", err)
	}
	if r2.ChunkCount == r1.ChunkCount {
		t.Logf("note: same chunk count after re-index — chunker may be coalescing; not a hard failure")
	}
	if idx.store.Count() != r2.ChunkCount {
		t.Errorf("after re-index: store has %d, expected %d (stale chunks not pruned)", idx.store.Count(), r2.ChunkCount)
	}
	if got := len(idx.Sources()); got != 1 {
		t.Errorf("Sources after re-index: got %d, want 1", got)
	}
}

func TestIndexerDeleteSource(t *testing.T) {
	idx, _ := newTestIndexer(t)
	f := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(f, []byte("hello world"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := idx.IndexPath(context.Background(), f); err != nil {
		t.Fatalf("IndexPath: %v", err)
	}
	abs, _ := filepath.Abs(f)
	if err := idx.DeleteSource(context.Background(), abs); err != nil {
		t.Fatalf("DeleteSource: %v", err)
	}
	if idx.store.Count() != 0 {
		t.Errorf("store should be empty after DeleteSource, has %d", idx.store.Count())
	}
	if len(idx.Sources()) != 0 {
		t.Errorf("Sources should be empty after DeleteSource")
	}
}

// countingEmbedder wraps stubEmbedder to track how many calls EmbedBatch
// received, so the mtime-skip test can prove a re-index of unchanged files
// doesn't hit the embedder.
type countingEmbedder struct {
	inner *stubEmbedder
	calls int
	items int
}

func (c *countingEmbedder) Name() string    { return c.inner.Name() }
func (c *countingEmbedder) Dimensions() int { return c.inner.Dimensions() }
func (c *countingEmbedder) Embed(ctx context.Context, t string) ([]float32, error) {
	c.calls++
	c.items++
	return c.inner.Embed(ctx, t)
}
func (c *countingEmbedder) EmbedBatch(ctx context.Context, ts []string) ([][]float32, error) {
	c.calls++
	c.items += len(ts)
	return c.inner.EmbedBatch(ctx, ts)
}

func TestIndexerSkipsUnchangedFiles(t *testing.T) {
	dir := t.TempDir()
	emb := &countingEmbedder{inner: &stubEmbedder{dim: 4}}
	store, err := NewChromemStore(dir, "test", emb.Dimensions())
	if err != nil {
		t.Fatalf("NewChromemStore: %v", err)
	}
	idx, err := NewIndexer(emb, store, IndexerOptions{
		SourcesPath: filepath.Join(dir, "sources.json"),
	})
	if err != nil {
		t.Fatalf("NewIndexer: %v", err)
	}
	defer idx.Close()

	src := t.TempDir()
	stableFile := filepath.Join(src, "stable.md")
	changedFile := filepath.Join(src, "changed.md")
	if err := os.WriteFile(stableFile, []byte("stable content here"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(changedFile, []byte("v1 content"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	r1, err := idx.IndexPath(context.Background(), src)
	if err != nil {
		t.Fatalf("first index: %v", err)
	}
	if r1.FileCount != 2 || r1.EmbeddedFiles != 2 || r1.SkippedFiles != 0 {
		t.Fatalf("first index result: %+v", r1)
	}
	firstItems := emb.items

	// Modify only `changed.md`; bump its mtime via os.Chtimes to be
	// explicit (some filesystems carry sub-second precision the indexer
	// must compare correctly).
	later := time.Now().Add(1 * time.Second)
	if err := os.WriteFile(changedFile, []byte("v2 content rewritten"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if err := os.Chtimes(changedFile, later, later); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	r2, err := idx.IndexPath(context.Background(), src)
	if err != nil {
		t.Fatalf("re-index: %v", err)
	}
	if r2.SkippedFiles != 1 {
		t.Errorf("SkippedFiles: got %d, want 1 (stable.md should have been reused)", r2.SkippedFiles)
	}
	if r2.EmbeddedFiles != 1 {
		t.Errorf("EmbeddedFiles: got %d, want 1 (changed.md only)", r2.EmbeddedFiles)
	}
	if emb.items <= firstItems {
		t.Errorf("embedder should have processed at least one new item on re-index, items=%d first=%d", emb.items, firstItems)
	}
	// Critically: a full re-embed would have called for both files; assert
	// the second pass embedded strictly less than the first.
	if delta := emb.items - firstItems; delta >= firstItems {
		t.Errorf("expected fewer embedded items on re-index than first run; first=%d delta=%d", firstItems, delta)
	}
}

func TestIndexerSidecarPersists(t *testing.T) {
	dir := t.TempDir()
	emb := &stubEmbedder{dim: 4}
	store, _ := NewChromemStore(dir, "test", emb.Dimensions())
	sidecar := filepath.Join(dir, "sources.json")
	idx, _ := NewIndexer(emb, store, IndexerOptions{SourcesPath: sidecar})

	f := filepath.Join(t.TempDir(), "doc.md")
	os.WriteFile(f, []byte("seed content"), 0o644)
	idx.IndexPath(context.Background(), f)
	if _, err := os.Stat(sidecar); err != nil {
		t.Fatalf("sidecar not written: %v", err)
	}
	idx.Close()

	// New indexer pointed at the same dir should rehydrate the source list
	// from the sidecar without re-indexing.
	store2, _ := NewChromemStore(dir, "test", emb.Dimensions())
	idx2, err := NewIndexer(emb, store2, IndexerOptions{SourcesPath: sidecar})
	if err != nil {
		t.Fatalf("rehydrate: %v", err)
	}
	defer idx2.Close()
	if len(idx2.Sources()) != 1 {
		t.Errorf("rehydrated indexer should see 1 source, got %d", len(idx2.Sources()))
	}
}
