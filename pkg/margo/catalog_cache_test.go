package margo

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeLister struct {
	models []Model
	err    error
	calls  int
}

func (f *fakeLister) ListModels(context.Context) ([]Model, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.models, nil
}

func f64(v float64) *float64 { return &v }

func TestNewCatalogCacheSeedsFromEmbedded(t *testing.T) {
	c := NewCatalogCache("", 0)
	for provider := range DefaultCatalog {
		if len(c.ModelIDs(provider)) == 0 {
			t.Errorf("provider %q has no seeded models", provider)
		}
	}
}

// The whole point of the overlay: OpenAI's endpoint returns ids only, so
// the context window and price have to survive the merge.
func TestMergeLiveFillsMissingMetadataFromOverlay(t *testing.T) {
	live := []Model{{ID: "claude-haiku-4-5"}} // as if the endpoint gave ids only
	got, err := MergeLive("anthropic", live)
	if err != nil {
		t.Fatalf("MergeLive: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d models, want 1", len(got))
	}
	if got[0].ContextTokens != 200000 {
		t.Errorf("context window lost in merge: %d", got[0].ContextTokens)
	}
	if got[0].CostPerMTokIn == nil {
		t.Error("price lost in merge")
	}
	if !got[0].Multimodal {
		t.Error("multimodal flag lost in merge")
	}
}

// Live data is authoritative wherever it actually says something.
func TestMergeLivePrefersLiveValues(t *testing.T) {
	live := []Model{{
		ID:             "claude-haiku-4-5",
		ContextTokens:  999_000,
		Multimodal:     true,
		CostPerMTokIn:  f64(1.5),
		CostPerMTokOut: f64(7.5),
	}}
	got, err := MergeLive("anthropic", live)
	if err != nil {
		t.Fatalf("MergeLive: %v", err)
	}
	if got[0].ContextTokens != 999_000 {
		t.Errorf("live context window ignored: %d", got[0].ContextTokens)
	}
	if *got[0].CostPerMTokIn != 1.5 {
		t.Errorf("live price ignored: %v", *got[0].CostPerMTokIn)
	}
}

// A model the provider no longer serves must disappear from the picker.
func TestMergeLiveDropsOverlayOnlyModels(t *testing.T) {
	live := []Model{{ID: "claude-haiku-4-5"}}
	got, err := MergeLive("anthropic", live)
	if err != nil {
		t.Fatalf("MergeLive: %v", err)
	}
	for _, m := range got {
		if m.ID == "claude-opus-4-7" {
			t.Fatal("a model absent from the live response survived the merge")
		}
	}
}

// An unknown id still gets a usable window rather than zero, or the
// budget rewriter would treat the whole conversation as unbounded.
func TestMergeLiveGivesUnknownModelAFallbackWindow(t *testing.T) {
	got, err := MergeLive("openai", []Model{{ID: "gpt-9-brand-new"}})
	if err != nil {
		t.Fatalf("MergeLive: %v", err)
	}
	if got[0].ContextTokens != defaultContextTokens {
		t.Errorf("context window = %d, want the %d fallback", got[0].ContextTokens, defaultContextTokens)
	}
}

// Curated ids keep their order; everything else sorts after them.
func TestMergeLiveOrdersCuratedFirst(t *testing.T) {
	got, err := MergeLive("anthropic", []Model{
		{ID: "zzz-experimental"},
		{ID: "claude-sonnet-4-6"},
		{ID: "aaa-experimental"},
		{ID: "claude-haiku-4-5"},
	})
	if err != nil {
		t.Fatalf("MergeLive: %v", err)
	}
	want := []string{"claude-haiku-4-5", "claude-sonnet-4-6", "aaa-experimental", "zzz-experimental"}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("order[%d] = %s, want %s (full: %v)", i, got[i].ID, id, ids(got))
		}
	}
}

// An empty response is a failed fetch, not an empty catalog. Without
// this the picker would blank out on a provider hiccup.
func TestMergeLiveRejectsEmptyResponse(t *testing.T) {
	if _, err := MergeLive("anthropic", nil); err == nil {
		t.Fatal("expected an error for an empty live response")
	}
	if _, err := MergeLive("anthropic", []Model{{ID: ""}}); err == nil {
		t.Fatal("expected an error when every entry is unusable")
	}
}

func TestRefreshStoresAndIndexes(t *testing.T) {
	c := NewCatalogCache(t.TempDir(), time.Hour)
	lister := &fakeLister{models: []Model{
		{ID: "claude-haiku-4-5", ContextTokens: 200000},
		{ID: "new-model", ContextTokens: 42},
	}}
	if err := c.Refresh(context.Background(), "anthropic", lister); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := c.ModelIDs("anthropic"); len(got) != 2 {
		t.Fatalf("ModelIDs = %v, want 2 entries", got)
	}
	m, ok := c.Lookup("new-model")
	if !ok || m.ContextTokens != 42 {
		t.Errorf("Lookup(new-model) = %+v, %v", m, ok)
	}
}

// A failing fetch must leave the previous catalog intact.
func TestRefreshFailureKeepsPreviousCatalog(t *testing.T) {
	c := NewCatalogCache("", time.Hour)
	before := c.ModelIDs("anthropic")

	err := c.Refresh(context.Background(), "anthropic", &fakeLister{err: errors.New("429 rate limited")})
	if err == nil {
		t.Fatal("expected the fetch error to propagate")
	}
	after := c.ModelIDs("anthropic")
	if len(after) != len(before) {
		t.Fatalf("catalog changed on a failed refresh: %v -> %v", before, after)
	}
}

func TestRefreshNilListerErrors(t *testing.T) {
	c := NewCatalogCache("", time.Hour)
	if err := c.Refresh(context.Background(), "anthropic", nil); err == nil {
		t.Fatal("expected an error for a nil lister")
	}
}

func TestStaleAndRefreshIfStale(t *testing.T) {
	c := NewCatalogCache("", time.Hour)
	if !c.Stale("anthropic") {
		t.Fatal("a never-fetched provider should be stale")
	}

	lister := &fakeLister{models: []Model{{ID: "claude-haiku-4-5"}}}
	did, err := c.RefreshIfStale(context.Background(), "anthropic", lister)
	if err != nil || !did {
		t.Fatalf("RefreshIfStale = %v, %v; want true, nil", did, err)
	}
	if c.Stale("anthropic") {
		t.Fatal("provider still stale immediately after a refresh")
	}

	did, err = c.RefreshIfStale(context.Background(), "anthropic", lister)
	if err != nil || did {
		t.Fatalf("second RefreshIfStale = %v, %v; want false, nil", did, err)
	}
	if lister.calls != 1 {
		t.Errorf("lister called %d times, want 1", lister.calls)
	}
}

// The cache has to survive a restart, or the TTL buys nothing.
func TestCachePersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	c1 := NewCatalogCache(dir, time.Hour)
	if err := c1.Refresh(context.Background(), "anthropic", &fakeLister{models: []Model{
		{ID: "persisted-model", ContextTokens: 7},
	}}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	c2 := NewCatalogCache(dir, time.Hour)
	m, ok := c2.Lookup("persisted-model")
	if !ok || m.ContextTokens != 7 {
		t.Fatalf("cached model did not survive: %+v %v", m, ok)
	}
	if c2.Stale("anthropic") {
		t.Error("a freshly cached provider read back as stale")
	}
}

// A corrupt cache file must fall back to the embedded seed rather than
// leaving the app with no models.
func TestCorruptCacheFileFallsBackToSeed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "models-anthropic.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	c := NewCatalogCache(dir, time.Hour)
	if len(c.ModelIDs("anthropic")) == 0 {
		t.Fatal("corrupt cache wiped the catalog")
	}
}

func TestMemoryOnlyCacheWritesNothing(t *testing.T) {
	dir := t.TempDir()
	c := NewCatalogCache("", time.Hour)
	if err := c.Refresh(context.Background(), "anthropic", &fakeLister{models: []Model{{ID: "x"}}}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("memory-only cache wrote %d files", len(entries))
	}
}

// LookupModel prefers the active cache so BudgetForModel sees live
// context windows.
func TestLookupModelPrefersActiveCatalog(t *testing.T) {
	t.Cleanup(func() { SetActiveCatalog(nil) })

	c := NewCatalogCache("", time.Hour)
	if err := c.Refresh(context.Background(), "anthropic", &fakeLister{models: []Model{
		{ID: "claude-haiku-4-5", ContextTokens: 5_000_000},
	}}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	SetActiveCatalog(c)

	m, ok := LookupModel("claude-haiku-4-5")
	if !ok {
		t.Fatal("LookupModel missed a model in the active catalog")
	}
	if m.ContextTokens != 5_000_000 {
		t.Errorf("context window = %d, want the live 5000000", m.ContextTokens)
	}
}

func TestLookupModelFallsBackToEmbedded(t *testing.T) {
	SetActiveCatalog(nil)
	if _, ok := LookupModel("claude-haiku-4-5"); !ok {
		t.Fatal("embedded fallback did not resolve a seeded model")
	}
	if _, ok := LookupModel("definitely-not-a-model"); ok {
		t.Fatal("resolved a model that does not exist")
	}
}

func ids(ms []Model) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

// Anthropic's catalog endpoint returns dated snapshots
// (claude-haiku-4-5-20251001) while models.json seeds the alias
// (claude-haiku-4-5). Before aliasFor, the snapshot inherited nothing:
// unpriced, fallback context window, and no curated rank — which moved
// the picker default from Haiku to Sonnet, a 4x price difference.
func TestMergeLiveResolvesDatedSnapshotToItsAlias(t *testing.T) {
	got, err := MergeLive("anthropic", []Model{{ID: "claude-haiku-4-5-20251001"}})
	if err != nil {
		t.Fatalf("MergeLive: %v", err)
	}
	m := got[0]
	if m.ContextTokens != 200000 {
		t.Errorf("context window = %d, want the alias's 200000", m.ContextTokens)
	}
	if m.CostPerMTokIn == nil || *m.CostPerMTokIn != 0.80 {
		t.Errorf("input price = %v, want the alias's 0.80", m.CostPerMTokIn)
	}
	if !m.Multimodal {
		t.Error("multimodal flag not inherited from the alias")
	}
}

// The snapshot must also inherit the alias's curated position, or the
// cheap default sinks below the expensive one.
func TestMergeLiveSnapshotInheritsCuratedRank(t *testing.T) {
	got, err := MergeLive("anthropic", []Model{
		{ID: "claude-opus-4-7"},
		{ID: "claude-sonnet-4-6"},
		{ID: "claude-haiku-4-5-20251001"},
	})
	if err != nil {
		t.Fatalf("MergeLive: %v", err)
	}
	if got[0].ID != "claude-haiku-4-5-20251001" {
		t.Fatalf("order = %v; the Haiku snapshot should hold the alias's first slot", ids(got))
	}
}

// An exact live id must still win over any alias match.
func TestMergeLivePrefersExactOverAlias(t *testing.T) {
	got, err := MergeLive("anthropic", []Model{
		{ID: "claude-haiku-4-5", ContextTokens: 111},
	})
	if err != nil {
		t.Fatalf("MergeLive: %v", err)
	}
	if got[0].ContextTokens != 111 {
		t.Errorf("context window = %d, want the live 111", got[0].ContextTokens)
	}
}

func TestAliasFor(t *testing.T) {
	overlay := map[string]Model{
		"claude-haiku-4-5": {ID: "claude-haiku-4-5"},
		"claude-opus-4-5":  {ID: "claude-opus-4-5"},
	}
	cases := []struct {
		id, want string
	}{
		{"claude-haiku-4-5-20251001", "claude-haiku-4-5"},
		{"claude-opus-4-5-20251101", "claude-opus-4-5"},
		// Not a date-shaped suffix: must not be claimed.
		{"claude-opus-4-50", ""},
		{"claude-haiku-4-5-preview", ""},
		{"claude-haiku-4-5-2025100", ""},
		{"claude-haiku-4-5", ""}, // exact ids are handled before aliasFor
		{"something-else-20251001", ""},
	}
	for _, tc := range cases {
		got, ok := aliasFor(tc.id, overlay)
		if tc.want == "" && ok {
			t.Errorf("aliasFor(%q) = %q, want no match", tc.id, got)
		}
		if tc.want != "" && got != tc.want {
			t.Errorf("aliasFor(%q) = %q, want %q", tc.id, got, tc.want)
		}
	}
}

// The cache holds merged output, so a cache written under different
// merge semantics has to be discarded rather than served.
func TestCacheIgnoresForeignVersion(t *testing.T) {
	dir := t.TempDir()
	stale, err := json.Marshal(map[string]any{
		"version":   catalogCacheVersion - 1,
		"provider":  "anthropic",
		"fetchedAt": time.Now(),
		"models":    []Model{{ID: "stale-model", ContextTokens: 1}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models-anthropic.json"), stale, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	c := NewCatalogCache(dir, time.Hour)
	if _, ok := c.Lookup("stale-model"); ok {
		t.Error("a cache from an older format version was served")
	}
	if !c.Stale("anthropic") {
		t.Error("provider should read as stale so the next refresh rebuilds it")
	}
	if len(c.ModelIDs("anthropic")) == 0 {
		t.Error("discarding the cache should fall back to the embedded seed, not to nothing")
	}
}

// A cache at the current version is still honoured.
func TestCacheAcceptsCurrentVersion(t *testing.T) {
	dir := t.TempDir()
	c1 := NewCatalogCache(dir, time.Hour)
	if err := c1.Refresh(context.Background(), "anthropic", &fakeLister{models: []Model{
		{ID: "current-model", ContextTokens: 5},
	}}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	c2 := NewCatalogCache(dir, time.Hour)
	if _, ok := c2.Lookup("current-model"); !ok {
		t.Error("a current-version cache was rejected")
	}
}
