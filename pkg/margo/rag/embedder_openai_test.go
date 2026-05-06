package rag

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/shakfu/margo/internal/config"
)

// TestOpenAIEmbedderArgValidation exercises the input-validation paths
// that must work without a network round-trip. Anything reachable
// without an API key belongs here.
func TestOpenAIEmbedderArgValidation(t *testing.T) {
	e := NewOpenAIEmbedder("test-key")

	if got := e.Dimensions(); got != DefaultOpenAIEmbedDims {
		t.Errorf("Dimensions() = %d, want %d", got, DefaultOpenAIEmbedDims)
	}
	if got := e.Name(); got != "openai:"+DefaultOpenAIEmbedModel {
		t.Errorf("Name() = %q, want %q", got, "openai:"+DefaultOpenAIEmbedModel)
	}

	ctx := context.Background()
	if _, err := e.Embed(ctx, ""); err == nil {
		t.Error("Embed(\"\") = nil err, want non-nil")
	}
	if _, err := e.EmbedBatch(ctx, []string{"ok", ""}); err == nil {
		t.Error("EmbedBatch with empty entry = nil err, want non-nil")
	}

	// Empty batch is the single legal "no-op" path.
	out, err := e.EmbedBatch(ctx, nil)
	if err != nil || out != nil {
		t.Errorf("EmbedBatch(nil) = (%v, %v), want (nil, nil)", out, err)
	}
}

// TestOpenAIEmbedderOptions verifies that WithModel / WithDimensions
// reach the embedder. The actual HTTP call is exercised in
// TestOpenAIEmbedderLive.
func TestOpenAIEmbedderOptions(t *testing.T) {
	e := NewOpenAIEmbedder("test-key",
		WithModel("text-embedding-3-large"),
		WithDimensions(3072),
	)
	if e.model != "text-embedding-3-large" {
		t.Errorf("model = %q, want text-embedding-3-large", e.model)
	}
	if e.Dimensions() != 3072 {
		t.Errorf("Dimensions() = %d, want 3072", e.Dimensions())
	}
}

// TestOpenAIEmbedderLive hits the real OpenAI API. Skipped when no
// key is configured (CI without secrets, contributors without an
// account). Costs a fraction of a cent per run.
func TestOpenAIEmbedderLive(t *testing.T) {
	cfg, _ := config.Load()
	if cfg == nil || cfg.OpenAIAPIKey == "" {
		t.Skip("OPENAI_API_KEY not set; skipping live embedder test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	e := NewOpenAIEmbedder(cfg.OpenAIAPIKey)

	// Single embed.
	v, err := e.Embed(ctx, "hello world")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(v) != e.Dimensions() {
		t.Errorf("Embed: len(v)=%d, want %d", len(v), e.Dimensions())
	}
	if !looksNormalized(v) {
		// text-embedding-3-* returns L2-normalised vectors; this is a
		// cheap sanity check that we read the wire format correctly.
		t.Errorf("Embed: vector does not look normalised (||v|| not ~1)")
	}

	// Batch embed: three semantically different prompts. Each should
	// be 1536 dims; pairwise cosine similarities should be < 0.99
	// (i.e. they're not all the same vector).
	texts := []string{
		"the quick brown fox",
		"einsteinian general relativity",
		"recipe for sourdough",
	}
	out, err := e.EmbedBatch(ctx, texts)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(out) != len(texts) {
		t.Fatalf("EmbedBatch: len(out)=%d, want %d", len(out), len(texts))
	}
	for i, v := range out {
		if len(v) != e.Dimensions() {
			t.Errorf("EmbedBatch[%d]: len=%d, want %d", i, len(v), e.Dimensions())
		}
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if cos := cosine(out[i], out[j]); cos > 0.99 {
				t.Errorf("EmbedBatch: pairs (%d,%d) too similar (cos=%.3f); responses likely scrambled", i, j, cos)
			}
		}
	}

	// Single == batch-of-one consistency.
	single, err := e.Embed(ctx, texts[0])
	if err != nil {
		t.Fatalf("Embed(texts[0]): %v", err)
	}
	if cos := cosine(single, out[0]); cos < 0.999 {
		t.Errorf("single vs batch[0] differ (cos=%.4f); want ≈1.0", cos)
	}
}

func cosine(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var dot, na, nb float32
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / float32(math.Sqrt(float64(na))*math.Sqrt(float64(nb)))
}

func looksNormalized(v []float32) bool {
	var sum float32
	for _, x := range v {
		sum += x * x
	}
	mag := math.Sqrt(float64(sum))
	return mag > 0.95 && mag < 1.05
}

