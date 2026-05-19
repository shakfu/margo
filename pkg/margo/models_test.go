package margo

import "testing"

// TestDefaultCatalogParses asserts the embedded models.json was parsed at
// init() and contains the three providers the codebase assumes.
func TestDefaultCatalogParses(t *testing.T) {
	for _, p := range []string{"anthropic", "openai", "openrouter"} {
		if len(DefaultCatalog.Models(p)) == 0 {
			t.Errorf("DefaultCatalog has no models for provider %q", p)
		}
	}
}

// TestModelIDsPreservesOrder ensures the first model in each provider
// stays first across re-parses; the UI uses ModelIDs[0] as the default
// picker selection.
func TestModelIDsPreservesOrder(t *testing.T) {
	got := DefaultCatalog.ModelIDs("anthropic")
	if len(got) == 0 || got[0] != "claude-haiku-4-5" {
		t.Errorf("expected first anthropic model to be claude-haiku-4-5, got %v", got)
	}
}

func TestContextWindowKnown(t *testing.T) {
	if w := DefaultCatalog.ContextWindow("claude-opus-4-7"); w != 1_000_000 {
		t.Errorf("claude-opus-4-7 context window: want 1_000_000, got %d", w)
	}
}

func TestContextWindowUnknownReturnsZero(t *testing.T) {
	if w := DefaultCatalog.ContextWindow("not-a-real-model-xyz"); w != 0 {
		t.Errorf("unknown model: want 0 (caller applies fallback), got %d", w)
	}
}

func TestIsMultimodal(t *testing.T) {
	if !DefaultCatalog.IsMultimodal("claude-opus-4-7") {
		t.Errorf("claude-opus-4-7 should be multimodal")
	}
	// A text-only OpenRouter entry that does not declare multimodal.
	if DefaultCatalog.IsMultimodal("deepseek/deepseek-v3.2") {
		t.Errorf("deepseek-v3.2 is text-only; should not report multimodal")
	}
}
