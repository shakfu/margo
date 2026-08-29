// Model catalog: the reactive snapshot fetched from Go, plus the
// context-window / multimodal / cost helpers that read it.
//
// Split out of the former monolithic lib/store.ts. Import from
// `lib/store` (the index), not from these modules directly — the index
// is the module boundary consumers depend on.

import { writable } from 'svelte/store';

// Model catalog. Single source of truth lives in pkg/margo/models.json
// and reaches the frontend via the Wails-bound ModelsCatalog() method.
// The hand-mirrored CONTEXT_WINDOWS / MULTIMODAL_MODELS lists that
// previously lived here are retired — adding a model is now a one-file
// change to pkg/margo/models.json.
export interface CatalogModel {
  id: string;
  contextTokens: number;
  multimodal?: boolean;
  // Optional cost data. Absent (`undefined`) means rate unknown — UI
  // hides the cost meter for these. Explicit zero (e.g. free-tier
  // models) means $0 — UI renders "$0.00" deliberately.
  costPerMTokIn?: number;
  costPerMTokOut?: number;
  pricedAt?: string;
}

export type Catalog = Record<string, CatalogModel[]>;

// Reactive catalog store. Empty until initModelCatalog() resolves; the
// 128k default in contextWindowFor protects callers that read before
// the first Wails round-trip completes.
export const modelCatalog = writable<Catalog>({});

// initModelCatalog populates the catalog from Go. Idempotent; call once
// at app startup. Network/IPC failure leaves the empty default in place
// (logged, not surfaced — UI consumers fall back to safe defaults).
export async function initModelCatalog(): Promise<void> {
  try {
    const { ModelsCatalog } = await import('../../../wailsjs/go/main/App');
    const cat = (await ModelsCatalog()) as Catalog;
    modelCatalog.set(cat ?? {});
  } catch (e) {
    console.error('initModelCatalog: failed to load catalog from Go', e);
  }
}

// contextWindowFor returns the model's context-token budget from the
// catalog snapshot passed in. The catalog argument is explicit (not read
// from the store) so callers reactively re-compute when the store
// updates — pass $modelCatalog from a $: block.
export function contextWindowFor(model: string, catalog: Catalog): number {
  for (const ms of Object.values(catalog)) {
    const found = ms.find((m) => m.id === model);
    if (found) return found.contextTokens;
  }
  return 128_000;
}

// modelFor finds the catalog entry for a model id. Internal helper for
// the cost / multimodal / context helpers below.
function modelFor(model: string, catalog: Catalog): CatalogModel | undefined {
  for (const ms of Object.values(catalog)) {
    const found = ms.find((m) => m.id === model);
    if (found) return found;
  }
  return undefined;
}

// hasCost reports whether both input and output per-MTok rates are
// declared for this model. Free-tier models with explicit zero rates
// return true; rate-omitted models return false (UI hides the meter).
// Mirrors margo.Catalog.HasCost on the Go side.
export function hasCost(model: string, catalog: Catalog): boolean {
  const m = modelFor(model, catalog);
  return !!m && m.costPerMTokIn !== undefined && m.costPerMTokOut !== undefined;
}

// costFor returns the running USD cost for a chat's accumulated token
// usage against the named model. Returns 0 when rates are unknown;
// callers should gate on hasCost() to distinguish that from a real
// "$0" (free-tier model).
//
// Does not account for Anthropic prompt-cache discounts — see the
// Go-side Catalog.Cost doc comment for the same trade-off; the meter
// overestimates rather than underestimates.
export function costFor(model: string, tokensIn: number, tokensOut: number, catalog: Catalog): number {
  const m = modelFor(model, catalog);
  if (!m || m.costPerMTokIn === undefined || m.costPerMTokOut === undefined) return 0;
  return (tokensIn / 1_000_000) * m.costPerMTokIn + (tokensOut / 1_000_000) * m.costPerMTokOut;
}

// formatCost renders a USD cost with sensible precision. Sub-cent
// amounts get 4 decimals so a $0.0023 conversation reads accurately;
// larger amounts collapse to the conventional 2 decimals.
export function formatCost(usd: number): string {
  if (usd === 0) return '$0.00';
  if (usd < 0.01) return `$${usd.toFixed(4)}`;
  return `$${usd.toFixed(2)}`;
}

// isMultimodal reports whether the model accepts image input. Same
// reactivity contract as contextWindowFor: pass a catalog snapshot.
export function isMultimodal(model: string, catalog: Catalog): boolean {
  for (const ms of Object.values(catalog)) {
    const found = ms.find((m) => m.id === model);
    if (found) return !!found.multimodal;
  }
  return false;
}

// modelToRestore decides which model to apply when a provider's catalog
// becomes known, or '' to leave the current selection alone.
//
// The remembered pick has to be actively re-applied rather than merely
// recorded. Model changes in the Default workspace are session-scoped by
// design, so `settings.model` never sees them; without a restore step
// the selection is recorded on every pick and read back on none, and
// the picker snaps to the catalog default on the next launch.
//
// Returns '' when the catalog does not (yet) contain the remembered
// model, so a cold start showing only the embedded seed waits for the
// live catalog instead of falling back and overwriting the choice.
export function modelToRestore(
  provider: string,
  available: string[],
  remembered: Record<string, string> | undefined,
  current: string,
): string {
  if (!provider || available.length === 0) return '';
  const want = remembered?.[provider];
  if (!want || want === current) return '';
  return available.includes(want) ? want : '';
}
