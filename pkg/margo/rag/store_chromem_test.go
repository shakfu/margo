package rag

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// makeVec returns a deterministic dim-d vector dominated by axis i,
// with small noise on the others. Cosine-similar to other vectors
// dominated by the same axis; near-orthogonal to the rest.
func makeVec(dim, i int) []float32 {
	v := make([]float32, dim)
	for j := range v {
		v[j] = 0.01
	}
	v[i%dim] = 1.0
	return v
}

func TestChromemStore_InMemoryUpsertQuery(t *testing.T) {
	ctx := context.Background()
	s, err := NewChromemStore("", "test", 8)
	if err != nil {
		t.Fatalf("NewChromemStore: %v", err)
	}
	defer s.Close()

	if got := s.Name(); got != "chromem:test" {
		t.Errorf("Name = %q, want chromem:test", got)
	}
	if got := s.Dimensions(); got != 8 {
		t.Errorf("Dimensions = %d, want 8", got)
	}
	if got := s.Count(); got != 0 {
		t.Errorf("empty Count = %d, want 0", got)
	}

	docs := []Document{
		{ID: "a", Vector: makeVec(8, 0), Content: "alpha", Metadata: map[string]string{"src": "x"}},
		{ID: "b", Vector: makeVec(8, 1), Content: "bravo", Metadata: map[string]string{"src": "y"}},
		{ID: "c", Vector: makeVec(8, 2), Content: "charlie", Metadata: map[string]string{"src": "x"}},
	}
	if err := s.Upsert(ctx, docs); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if got := s.Count(); got != 3 {
		t.Errorf("Count after upsert = %d, want 3", got)
	}

	// Query with vector close to "a" — top result should be a.
	hits, err := s.Query(ctx, makeVec(8, 0), 2, nil)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("Query returned %d hits, want 2", len(hits))
	}
	if hits[0].ID != "a" {
		t.Errorf("top hit = %q, want a", hits[0].ID)
	}
	if hits[0].Score < hits[1].Score {
		t.Errorf("results not sorted by score desc: %.3f then %.3f", hits[0].Score, hits[1].Score)
	}
	if hits[0].Score < 0.5 {
		t.Errorf("self-query score = %.3f, want >0.5 for axis-aligned vectors", hits[0].Score)
	}

	// Query with metadata filter — only docs tagged src=x.
	hits, err = s.Query(ctx, makeVec(8, 1), 5, map[string]string{"src": "x"})
	if err != nil {
		t.Fatalf("Query with where: %v", err)
	}
	for _, h := range hits {
		if h.Metadata["src"] != "x" {
			t.Errorf("filter leaked doc with src=%q", h.Metadata["src"])
		}
	}
}

func TestChromemStore_UpsertReplaces(t *testing.T) {
	ctx := context.Background()
	s, _ := NewChromemStore("", "test", 4)
	defer s.Close()

	if err := s.Upsert(ctx, []Document{{ID: "a", Vector: makeVec(4, 0), Content: "first"}}); err != nil {
		t.Fatalf("Upsert 1: %v", err)
	}
	if err := s.Upsert(ctx, []Document{{ID: "a", Vector: makeVec(4, 0), Content: "second"}}); err != nil {
		t.Fatalf("Upsert 2: %v", err)
	}
	if got := s.Count(); got != 1 {
		t.Errorf("Count after replace = %d, want 1 (no double-store)", got)
	}
	hits, err := s.Query(ctx, makeVec(4, 0), 1, nil)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(hits) != 1 || hits[0].Content != "second" {
		t.Errorf("Content = %q, want second (newer upsert)", hits[0].Content)
	}
}

func TestChromemStore_Delete(t *testing.T) {
	ctx := context.Background()
	s, _ := NewChromemStore("", "test", 4)
	defer s.Close()

	_ = s.Upsert(ctx, []Document{
		{ID: "a", Vector: makeVec(4, 0)},
		{ID: "b", Vector: makeVec(4, 1)},
	})
	if err := s.Delete(ctx, []string{"a", "missing"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := s.Count(); got != 1 {
		t.Errorf("Count after delete = %d, want 1", got)
	}
}

func TestChromemStore_DimensionMismatch(t *testing.T) {
	ctx := context.Background()
	s, _ := NewChromemStore("", "test", 4)
	defer s.Close()

	err := s.Upsert(ctx, []Document{{ID: "a", Vector: makeVec(8, 0)}})
	if err == nil {
		t.Fatal("Upsert with 8-dim vector into 4-dim store: nil err, want error")
	}
	if !strings.Contains(err.Error(), "8-dim") {
		t.Errorf("error %q does not mention vector size", err.Error())
	}

	_, err = s.Query(ctx, makeVec(8, 0), 1, nil)
	if err == nil {
		t.Fatal("Query with 8-dim vector against 4-dim store: nil err, want error")
	}

	if err := s.Upsert(ctx, []Document{{ID: "", Vector: makeVec(4, 0)}}); err == nil {
		t.Fatal("Upsert with empty ID: nil err, want error")
	}
}

func TestChromemStore_Persistence(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	scope := "ws-1"
	baseDir := filepath.Join(dir, scope)

	// Write some data, close.
	{
		s, err := NewChromemStore(baseDir, scope, 4)
		if err != nil {
			t.Fatalf("first NewChromemStore: %v", err)
		}
		err = s.Upsert(ctx, []Document{
			{ID: "a", Vector: makeVec(4, 0), Content: "alpha"},
			{ID: "b", Vector: makeVec(4, 1), Content: "bravo"},
		})
		if err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		if err := s.Persist(ctx); err != nil {
			t.Fatalf("Persist: %v", err)
		}
		_ = s.Close()
	}

	// Reopen the same baseDir; data should be there.
	s2, err := NewChromemStore(baseDir, scope, 4)
	if err != nil {
		t.Fatalf("reopen NewChromemStore: %v", err)
	}
	defer s2.Close()
	if got := s2.Count(); got != 2 {
		t.Errorf("Count after reopen = %d, want 2", got)
	}
	hits, err := s2.Query(ctx, makeVec(4, 0), 1, nil)
	if err != nil {
		t.Fatalf("Query after reopen: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "a" || hits[0].Content != "alpha" {
		t.Errorf("post-reopen top hit = %+v, want id=a content=alpha", hits[0])
	}
}

func TestWorkspaceVectorDir(t *testing.T) {
	if _, err := WorkspaceVectorDir(""); err == nil {
		t.Fatal("WorkspaceVectorDir(\"\") = nil err, want error")
	}
	got, err := WorkspaceVectorDir("ws-42")
	if err != nil {
		t.Fatalf("WorkspaceVectorDir: %v", err)
	}
	if !strings.HasSuffix(got, filepath.Join("Margo", "vectors", "ws-42")) {
		t.Errorf("path %q does not end with Margo/vectors/ws-42", got)
	}
}
