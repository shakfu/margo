package margo

import (
	_ "embed"
	"encoding/json"
)

// Model is one entry in the model catalog.
//
// Fields:
//
//   - ID              provider-native model identifier (matches the
//                     wire-level model parameter).
//   - ContextTokens   input-token context window. Used by
//                     agent/budget.go for history-rewrite decisions.
//   - Multimodal      true if the model accepts image input. Gates the
//                     frontend's paperclip affordance.
//   - CostPerMTokIn   optional USD price per million input tokens.
//                     Pointer so nil ≠ &0 — a free-tier model
//                     (rate set to zero) is distinguishable from an
//                     unknown-rate model (rate omitted entirely).
//                     The frontend hides the cost meter for chats on
//                     unknown-rate models rather than showing $0.00,
//                     which would be misleading.
//   - CostPerMTokOut  optional USD price per million output tokens.
//   - PricedAt        optional ISO date for when costs were last
//                     verified. Helps reviewers spot stale data.
type Model struct {
	ID             string   `json:"id"`
	ContextTokens  int      `json:"contextTokens"`
	Multimodal     bool     `json:"multimodal,omitempty"`
	CostPerMTokIn  *float64 `json:"costPerMTokIn,omitempty"`
	CostPerMTokOut *float64 `json:"costPerMTokOut,omitempty"`
	PricedAt       string   `json:"pricedAt,omitempty"`
}

// Catalog is the parsed shape of models.json: a map from provider name
// to an *ordered* list of models. Order is meaningful — the first entry
// is the provider's default model in any picker.
type Catalog map[string][]Model

//go:embed models.json
var modelsJSON []byte

// DefaultCatalog is the package-level parsed catalog, populated from the
// embedded models.json at process start. It is the single source of
// truth that both pkg/margo/core (provider/model surface) and
// pkg/margo/agent (context-window budget) read from; the frontend reads
// it via the Wails-bound ModelsCatalog() method so the prior hand-mirrored
// CONTEXT_WINDOWS / MULTIMODAL_MODELS lists in store.ts can be retired.
var DefaultCatalog Catalog

func init() {
	if err := json.Unmarshal(modelsJSON, &DefaultCatalog); err != nil {
		// Embedded JSON is build-time data; if it fails to parse the
		// binary is mis-built and there is no useful recovery.
		panic("margo: invalid embedded models.json: " + err.Error())
	}
}

// Models returns the models for a provider in catalog-declared order, or
// nil if the provider is unknown.
func (c Catalog) Models(provider string) []Model {
	return c[provider]
}

// ModelIDs returns just the ids for a provider, preserving order.
// Convenience for callers that don't need the metadata.
func (c Catalog) ModelIDs(provider string) []string {
	ms := c[provider]
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

// ContextWindow returns the context-token budget for a model id, looking
// across all providers. Returns 0 when the model is unknown so callers
// can apply their own fallback (BudgetForModel does this).
func (c Catalog) ContextWindow(id string) int {
	for _, ms := range c {
		for _, m := range ms {
			if m.ID == id {
				return m.ContextTokens
			}
		}
	}
	return 0
}

// IsMultimodal reports whether the named model accepts image input.
// Unknown models return false (conservative default; the frontend uses
// this to decide whether to expose attachment affordances).
func (c Catalog) IsMultimodal(id string) bool {
	for _, ms := range c {
		for _, m := range ms {
			if m.ID == id {
				return m.Multimodal
			}
		}
	}
	return false
}

// HasCost reports whether the named model has both input and output
// per-MTok rates declared in models.json. Used by callers that need to
// distinguish "free-tier zero" from "rate unknown" — both manifest as
// a zero result from Cost() but only the former is meaningful.
func (c Catalog) HasCost(id string) bool {
	for _, ms := range c {
		for _, m := range ms {
			if m.ID == id {
				return m.CostPerMTokIn != nil && m.CostPerMTokOut != nil
			}
		}
	}
	return false
}

// Cost returns the USD cost of a token-count usage against the named
// model. Returns 0 (and HasCost returns false) when rates are not
// declared — callers should check HasCost first if they need to
// distinguish "free / unknown" from "non-zero cost." Rates are
// per-million-tokens; this method does the divide.
//
// Cost does not differentiate between cached and uncached input tokens
// (Anthropic charges 10% for cache reads, 125% for cache writes); the
// usage parameter is the single InputTokens / OutputTokens pair
// returned by the provider, so the calculation assumes the uncached
// rate. Real-world cost may be lower for prompt-cached workloads;
// the meter overestimates, which is the safer error to display.
func (c Catalog) Cost(id string, inputTokens, outputTokens int) float64 {
	for _, ms := range c {
		for _, m := range ms {
			if m.ID != id {
				continue
			}
			if m.CostPerMTokIn == nil || m.CostPerMTokOut == nil {
				return 0
			}
			in := float64(inputTokens) / 1_000_000 * (*m.CostPerMTokIn)
			out := float64(outputTokens) / 1_000_000 * (*m.CostPerMTokOut)
			return in + out
		}
	}
	return 0
}
