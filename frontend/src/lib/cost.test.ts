// Tests for the cost-meter helpers in store.ts. These pure functions
// were added when the cost meter shipped; this file is the regression
// net for the pointer-vs-zero distinction (free-tier model with
// explicit $0 rates vs. rate-unknown model) and the formatting rule
// (sub-cent amounts get 4 decimals, larger amounts get 2).
//
// The Go-side mirror at pkg/margo/models_test.go covers the same logic
// for `Catalog.HasCost` / `Catalog.Cost`. Both must stay in sync for
// the frontend's running cost meter and the (future) TUI cost display
// to agree.

import { describe, test, expect } from 'vitest';
import { hasCost, costFor, formatCost, type Catalog } from './store';

// A small fixture that exercises every case the helpers branch on:
//   priced — both rates declared, non-zero
//   free   — both rates explicitly zero (free-tier model)
//   unknown — rate fields omitted (catalog hasn't been verified)
const fixture: Catalog = {
  anthropic: [
    { id: 'priced',  contextTokens: 200_000, multimodal: true, costPerMTokIn: 3,    costPerMTokOut: 15 },
    { id: 'free',    contextTokens: 128_000,                   costPerMTokIn: 0,    costPerMTokOut: 0 },
    { id: 'unknown', contextTokens: 128_000 },
  ],
};

describe('hasCost', () => {
  test('priced model reports true', () => {
    expect(hasCost('priced', fixture)).toBe(true);
  });
  test('free-tier model with explicit zero rates reports true', () => {
    expect(hasCost('free', fixture)).toBe(true);
  });
  test('rate-unknown model reports false', () => {
    expect(hasCost('unknown', fixture)).toBe(false);
  });
  test('unknown model id reports false', () => {
    expect(hasCost('does-not-exist', fixture)).toBe(false);
  });
  test('empty catalog reports false', () => {
    expect(hasCost('priced', {})).toBe(false);
  });
});

describe('costFor', () => {
  test('computes USD from per-MTok rates', () => {
    // priced: $3 in, $15 out per million. 1000 in + 500 out:
    //   1000/1e6 * 3 + 500/1e6 * 15 = 0.003 + 0.0075 = 0.0105
    expect(costFor('priced', 1000, 500, fixture)).toBeCloseTo(0.0105, 9);
  });
  test('returns 0 for free-tier model regardless of usage', () => {
    expect(costFor('free', 10000, 5000, fixture)).toBe(0);
  });
  test('returns 0 for rate-unknown model (UI gates via hasCost)', () => {
    expect(costFor('unknown', 10000, 5000, fixture)).toBe(0);
  });
  test('returns 0 for unknown model id', () => {
    expect(costFor('does-not-exist', 1000, 1000, fixture)).toBe(0);
  });
  test('zero tokens yields zero cost even for priced models', () => {
    expect(costFor('priced', 0, 0, fixture)).toBe(0);
  });
});

describe('formatCost', () => {
  test('exact zero gets two decimals — the meter says "$0.00" deliberately', () => {
    expect(formatCost(0)).toBe('$0.00');
  });
  test('sub-cent amounts get 4 decimals so $0.0023 reads accurately', () => {
    expect(formatCost(0.0023)).toBe('$0.0023');
    expect(formatCost(0.0001)).toBe('$0.0001');
    // Edge: just below the 1-cent threshold should still get 4 decimals.
    expect(formatCost(0.009)).toBe('$0.0090');
  });
  test('one-cent-or-more amounts collapse to 2 decimals', () => {
    expect(formatCost(0.01)).toBe('$0.01');
    expect(formatCost(1.234)).toBe('$1.23');
    expect(formatCost(42)).toBe('$42.00');
  });
});
