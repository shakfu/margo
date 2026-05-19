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

// TestHasCostDistinguishesUnknownFromFree confirms the nil-pointer
// convention does what the docs claim: rate-omitted entries report
// no cost; free-tier entries (rate = 0) report HasCost=true so
// callers can render them as "$0.00" rather than as "rate unknown".
func TestHasCostDistinguishesUnknownFromFree(t *testing.T) {
	// claude-* models in the embedded catalog have explicit rates.
	if !DefaultCatalog.HasCost("claude-opus-4-7") {
		t.Errorf("claude-opus-4-7 should have cost data declared")
	}
	// gpt-5.4-* entries are intentionally rate-unknown until OpenAI
	// pricing is verified.
	if DefaultCatalog.HasCost("gpt-5.4-nano") {
		t.Errorf("gpt-5.4-nano has no cost in the catalog; HasCost should be false")
	}
	// Free OpenRouter tier has explicit zero rates; HasCost=true.
	if !DefaultCatalog.HasCost("google/gemma-4-26b-a4b-it:free") {
		t.Errorf("free-tier model with explicit zero rates should report HasCost=true")
	}
	if DefaultCatalog.HasCost("not-a-real-model") {
		t.Errorf("unknown model should report HasCost=false")
	}
}

func TestCostCalculation(t *testing.T) {
	// claude-opus-4-7: $15 in, $75 out per million.
	// 1000 input + 500 output → 1000/1e6*15 + 500/1e6*75 = 0.015 + 0.0375 = 0.0525
	got := DefaultCatalog.Cost("claude-opus-4-7", 1000, 500)
	want := 0.0525
	if !approxEqual(got, want, 1e-9) {
		t.Errorf("Cost(opus, 1000, 500) = %v, want %v", got, want)
	}
	// Free-tier model: explicit zero rates → zero cost.
	if got := DefaultCatalog.Cost("google/gemma-4-26b-a4b-it:free", 10000, 5000); got != 0 {
		t.Errorf("Cost on free-tier model should be 0, got %v", got)
	}
	// Unknown-cost model: nil rates → zero cost.
	// HasCost(...) lets the UI distinguish this from a real $0.
	if got := DefaultCatalog.Cost("gpt-5.4-nano", 10000, 5000); got != 0 {
		t.Errorf("Cost on rate-unknown model should be 0, got %v", got)
	}
	// Unknown model id → zero cost.
	if got := DefaultCatalog.Cost("not-a-real-model", 1000, 1000); got != 0 {
		t.Errorf("Cost on unknown model id should be 0, got %v", got)
	}
}

func approxEqual(a, b, eps float64) bool {
	if a > b {
		return a-b < eps
	}
	return b-a < eps
}
