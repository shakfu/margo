package margo

import (
	_ "embed"
	"encoding/json"
)

// Model is one entry in the model catalog: the provider-specific id, the
// context-window budget the agent's budget rewriter uses to decide when
// to summarize history, and whether the model accepts image input. The
// frontend gates its paperclip / drop-zone affordances on Multimodal.
//
// Cost fields are intentionally omitted here; when a cost meter ships,
// extend this struct with optional `CostPerInputTokens` / `CostPerOutputTokens`
// fields and populate them in models.json — adding optional fields is
// backwards-compatible with existing consumers.
type Model struct {
	ID            string `json:"id"`
	ContextTokens int    `json:"contextTokens"`
	Multimodal    bool   `json:"multimodal,omitempty"`
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
