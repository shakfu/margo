package margo

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultCatalogTTL is how long a fetched provider catalog is reused
// before the next refresh. Model line-ups move on the order of weeks, so
// a day is generous; the user can force a refresh from the UI.
const DefaultCatalogTTL = 24 * time.Hour

// catalogCacheVersion stamps the on-disk format. Bump it whenever
// MergeLive's semantics change.
//
// The cache stores merged output, not the raw provider response, so a
// change to the merge leaves existing files frozen with the old
// derivation until their TTL expires. Version 2 added alias resolution
// for dated snapshots: without a bump, a cache written by version 1
// would keep serving an unpriced Haiku ranked below Sonnet.
const catalogCacheVersion = 2

// cacheFile is one provider's catalog as written to disk.
type cacheFile struct {
	Version   int       `json:"version"`
	Provider  string    `json:"provider"`
	FetchedAt time.Time `json:"fetchedAt"`
	Models    []Model   `json:"models"`
}

// CatalogCache resolves a provider's model list from, in order of
// preference: a live fetch, a fresh-enough disk cache, and the embedded
// models.json. The embedded file is the offline seed and the metadata
// overlay, not the source of truth — see MergeLive.
//
// Every method is safe for concurrent use. Lookups are served from an
// id-keyed index rebuilt on each mutation, because the frontend's cost
// meter calls Cost per render and OpenRouter's catalog is ~400 entries;
// a linear scan per token was fine at 26.
type CatalogCache struct {
	dir string
	ttl time.Duration

	mu        sync.RWMutex
	models    map[string][]Model // provider -> ordered models
	fetchedAt map[string]time.Time
	byID      map[string]Model // flattened index for lookups
}

// NewCatalogCache returns a cache seeded from the embedded catalog and
// then overlaid with whatever is already on disk. dir is where per-
// provider caches live; pass "" to run memory-only (tests, and any
// caller with no writable config directory).
func NewCatalogCache(dir string, ttl time.Duration) *CatalogCache {
	if ttl <= 0 {
		ttl = DefaultCatalogTTL
	}
	c := &CatalogCache{
		dir:       dir,
		ttl:       ttl,
		models:    map[string][]Model{},
		fetchedAt: map[string]time.Time{},
	}
	for provider, ms := range DefaultCatalog {
		c.models[provider] = append([]Model(nil), ms...)
	}
	c.loadFromDisk()
	c.reindex()
	return c
}

// DefaultCatalogDir returns the per-user directory holding cached
// provider catalogs. Callers that cannot determine it should pass "" to
// NewCatalogCache and run memory-only rather than failing.
func DefaultCatalogDir() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("user config dir: %w", err)
	}
	return filepath.Join(cfg, "Margo", "catalog"), nil
}

// Catalog returns a snapshot of every provider's current model list.
func (c *CatalogCache) Catalog() Catalog {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(Catalog, len(c.models))
	for p, ms := range c.models {
		out[p] = append([]Model(nil), ms...)
	}
	return out
}

// Models returns one provider's models in display order.
func (c *CatalogCache) Models(provider string) []Model {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Model(nil), c.models[provider]...)
}

// ModelIDs returns just the ids for a provider, preserving order.
func (c *CatalogCache) ModelIDs(provider string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ms := c.models[provider]
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

// Lookup returns the catalog entry for an id across every provider.
func (c *CatalogCache) Lookup(id string) (Model, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m, ok := c.byID[id]
	return m, ok
}

// Stale reports whether provider's catalog is older than the TTL. A
// provider never fetched is stale.
func (c *CatalogCache) Stale(provider string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	at, ok := c.fetchedAt[provider]
	return !ok || time.Since(at) > c.ttl
}

// Refresh fetches provider's catalog through lister and stores the
// merged result. A fetch failure leaves the previous contents in place
// and returns the error, so a provider that is down or rate-limited
// degrades to the last good list rather than to an empty picker.
func (c *CatalogCache) Refresh(ctx context.Context, provider string, lister ModelLister) error {
	if lister == nil {
		return fmt.Errorf("provider %q cannot list models", provider)
	}
	live, err := lister.ListModels(ctx)
	if err != nil {
		return err
	}
	merged, err := MergeLive(provider, live)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.models[provider] = merged
	c.fetchedAt[provider] = time.Now()
	c.mu.Unlock()
	c.reindex()

	c.writeToDisk(provider, merged)
	return nil
}

// RefreshIfStale is Refresh gated on the TTL. Returns false when the
// cache was already fresh and no call was made.
func (c *CatalogCache) RefreshIfStale(ctx context.Context, provider string, lister ModelLister) (bool, error) {
	if !c.Stale(provider) {
		return false, nil
	}
	return true, c.Refresh(ctx, provider, lister)
}

// MergeLive combines a provider's live model list with the embedded
// catalog, which supplies whatever the endpoint did not report.
//
// The merge exists because the three endpoints are not equivalent.
// OpenRouter reports context length, modality, and both prices.
// Anthropic reports everything but price. OpenAI reports identifiers and
// nothing else. Without an overlay, switching to live data would silently
// blank the context windows the budget rewriter depends on and hide the
// cost meter for every OpenAI model.
//
// Live wins for any field it actually populates; the overlay fills the
// rest. Models present in the overlay but absent from the live response
// are dropped — the provider is authoritative about what exists.
// A live response with no usable entries is rejected so a malformed or
// empty fetch cannot replace a working catalog with nothing.
func MergeLive(provider string, live []Model) ([]Model, error) {
	overlay := map[string]Model{}
	for _, m := range DefaultCatalog[provider] {
		overlay[m.ID] = m
	}
	// Overlay order is the curated one; live ids not in it sort after,
	// alphabetically, so the picker's head stays stable across refreshes.
	rank := map[string]int{}
	for i, m := range DefaultCatalog[provider] {
		rank[m.ID] = i
	}

	// rankOf maps a live id to the curated position it inherited, which
	// may come from its alias rather than from itself.
	rankOf := map[string]int{}

	out := make([]Model, 0, len(live))
	for _, m := range live {
		if m.ID == "" {
			continue
		}
		base, baseID := overlay[m.ID], m.ID
		if _, exact := overlay[m.ID]; !exact {
			// Anthropic's catalog endpoint returns dated snapshots
			// (claude-haiku-4-5-20251001) while models.json seeds the
			// alias (claude-haiku-4-5). Without this the snapshot
			// inherits nothing: no price, no real context window, and
			// no curated rank — which silently moved the default model
			// from Haiku to Sonnet, a 4x price difference.
			if alias, ok := aliasFor(m.ID, overlay); ok {
				base, baseID = overlay[alias], alias
			}
		}
		merged := Model{
			ID:             m.ID,
			ContextTokens:  firstNonZero(m.ContextTokens, base.ContextTokens, defaultContextTokens),
			Multimodal:     m.Multimodal || base.Multimodal,
			CostPerMTokIn:  firstNonNil(m.CostPerMTokIn, base.CostPerMTokIn),
			CostPerMTokOut: firstNonNil(m.CostPerMTokOut, base.CostPerMTokOut),
			PricedAt:       firstNonEmpty(m.PricedAt, base.PricedAt),
		}
		if r, ok := rank[baseID]; ok {
			rankOf[m.ID] = r
		}
		out = append(out, merged)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("provider %q returned no usable models", provider)
	}

	sort.SliceStable(out, func(i, j int) bool {
		ri, iok := rankOf[out[i].ID]
		rj, jok := rankOf[out[j].ID]
		switch {
		case iok && jok:
			return ri < rj
		case iok:
			return true
		case jok:
			return false
		default:
			return out[i].ID < out[j].ID
		}
	})
	return out, nil
}

// snapshotSuffix matches the `-YYYYMMDD` a provider appends to pin a
// dated snapshot of an aliased model.
var snapshotSuffix = regexp.MustCompile(`^-\d{8}$`)

// aliasFor finds the overlay entry a dated snapshot id belongs to:
// "claude-haiku-4-5-20251001" resolves to "claude-haiku-4-5".
//
// The date-shaped suffix is required rather than accepting any prefix,
// so "claude-opus-4-5" cannot claim "claude-opus-4-50".
func aliasFor(id string, overlay map[string]Model) (string, bool) {
	best := ""
	for alias := range overlay {
		if len(alias) >= len(id) || !strings.HasPrefix(id, alias) {
			continue
		}
		if !snapshotSuffix.MatchString(id[len(alias):]) {
			continue
		}
		if len(alias) > len(best) {
			best = alias
		}
	}
	return best, best != ""
}

// defaultContextTokens is the fallback when neither the provider nor the
// overlay declares a window. Matches agent.defaultContextWindow: low
// enough that an unknown model does not silently overflow.
const defaultContextTokens = 128_000

func firstNonZero(vals ...int) int {
	for _, v := range vals {
		if v > 0 {
			return v
		}
	}
	return 0
}

func firstNonNil(vals ...*float64) *float64 {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// reindex rebuilds the id lookup table. Called after every mutation;
// lookups then cost a map hit instead of a scan over every provider.
func (c *CatalogCache) reindex() {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx := make(map[string]Model, 512)
	for _, ms := range c.models {
		for _, m := range ms {
			idx[m.ID] = m
		}
	}
	c.byID = idx
}

func (c *CatalogCache) cachePath(provider string) string {
	return filepath.Join(c.dir, "models-"+provider+".json")
}

// loadFromDisk overlays any cached catalogs onto the embedded seed. A
// missing, unreadable, or malformed file is not an error: the seed is
// already in place and is a valid catalog.
func (c *CatalogCache) loadFromDisk() {
	if c.dir == "" {
		return
	}
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(c.dir, e.Name()))
		if err != nil {
			continue
		}
		var cf cacheFile
		if err := json.Unmarshal(raw, &cf); err != nil || cf.Provider == "" || len(cf.Models) == 0 {
			continue
		}
		if cf.Version != catalogCacheVersion {
			// Written by a different merge. Ignore it and leave the
			// provider stale so the next refresh rebuilds it.
			continue
		}
		c.models[cf.Provider] = cf.Models
		c.fetchedAt[cf.Provider] = cf.FetchedAt
	}
}

// writeToDisk persists one provider's catalog. Best-effort: a failed
// write costs a refetch next launch, which is not worth failing a call
// the user did not ask for.
func (c *CatalogCache) writeToDisk(provider string, models []Model) {
	if c.dir == "" {
		return
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return
	}
	raw, err := json.MarshalIndent(cacheFile{
		Version:   catalogCacheVersion,
		Provider:  provider,
		FetchedAt: time.Now().UTC(),
		Models:    models,
	}, "", "  ")
	if err != nil {
		return
	}
	// Write-then-rename so a crash mid-write cannot leave a truncated
	// file that loadFromDisk would silently skip on the next launch.
	tmp := c.cachePath(provider) + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, c.cachePath(provider))
}

// The active catalog. DefaultCatalog stays the immutable embedded seed;
// this points at whatever CatalogCache the running process built, so the
// budget rewriter and any other id-keyed lookup see live context windows
// rather than whatever was true at build time.
//
// A package-level pointer rather than a threaded parameter because the
// readers (agent.BudgetForModel) are called from deep inside runner
// middleware that has no Session to hand. Reads that were already
// package-global stay package-global; nothing new becomes mutable that
// was not already.
var (
	activeMu    sync.RWMutex
	activeCache *CatalogCache
)

// SetActiveCatalog installs c as the process-wide lookup source.
func SetActiveCatalog(c *CatalogCache) {
	activeMu.Lock()
	activeCache = c
	activeMu.Unlock()
}

// LookupModel resolves a model id against the active catalog, falling
// back to the embedded seed when no cache has been installed (tests, and
// any caller using pkg/margo as a bare library).
func LookupModel(id string) (Model, bool) {
	activeMu.RLock()
	c := activeCache
	activeMu.RUnlock()
	if c != nil {
		if m, ok := c.Lookup(id); ok {
			return m, true
		}
	}
	for _, ms := range DefaultCatalog {
		for _, m := range ms {
			if m.ID == id {
				return m, true
			}
		}
	}
	return Model{}, false
}
