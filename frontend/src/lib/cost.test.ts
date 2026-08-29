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
import { hasCost, costFor, formatCost, costBreakdown, modelToRestore, type Catalog, type Message } from './store';

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

// costBreakdown prices each assistant turn against the model that
// produced it. The bug it replaces: cost was the chat's running token
// totals priced against whatever model was selected at render time, so
// switching model mid-chat repriced the whole history.

const priced: Catalog = {
  anthropic: [
    { id: 'cheap', contextTokens: 1000, costPerMTokIn: 1, costPerMTokOut: 2 },
    { id: 'dear', contextTokens: 1000, costPerMTokIn: 100, costPerMTokOut: 200 },
  ],
  openai: [{ id: 'norate', contextTokens: 1000 }],
};

function turn(model: string | undefined, provider: string, tin: number, tout: number): Message {
  return {
    role: 'assistant',
    content: '',
    model,
    provider,
    usage: { inputTokens: tin, outputTokens: tout, firstTokenMs: 0, totalMs: 0 },
  } as Message;
}

describe('costBreakdown', () => {
  test('prices each turn against its own model, not the current one', () => {
    // 1M tokens on the cheap model, 1M on the dear one.
    const got = costBreakdown(
      [turn('cheap', 'anthropic', 1_000_000, 0), turn('dear', 'anthropic', 1_000_000, 0)],
      priced,
    );
    // $1 + $100. Repricing everything as 'dear' would give $200.
    expect(got.total).toBeCloseTo(101, 6);
  });

  test('groups repeated turns on the same model', () => {
    const got = costBreakdown(
      [turn('cheap', 'anthropic', 1_000_000, 0), turn('cheap', 'anthropic', 1_000_000, 0)],
      priced,
    );
    expect(got.entries).toHaveLength(1);
    expect(got.entries[0].tokensIn).toBe(2_000_000);
    expect(got.total).toBeCloseTo(2, 6);
  });

  test('reports multi-model and multi-provider use', () => {
    const single = costBreakdown([turn('cheap', 'anthropic', 10, 10)], priced);
    expect(single.multiModel).toBe(false);
    expect(single.multiProvider).toBe(false);

    const twoModels = costBreakdown(
      [turn('cheap', 'anthropic', 10, 10), turn('dear', 'anthropic', 10, 10)],
      priced,
    );
    expect(twoModels.multiModel).toBe(true);
    expect(twoModels.multiProvider).toBe(false);

    const twoProviders = costBreakdown(
      [turn('cheap', 'anthropic', 10, 10), turn('norate', 'openai', 10, 10)],
      priced,
    );
    expect(twoProviders.multiProvider).toBe(true);
  });

  test('counts unpriced models but excludes them from the total', () => {
    const got = costBreakdown(
      [turn('cheap', 'anthropic', 1_000_000, 0), turn('norate', 'openai', 1_000_000, 0)],
      priced,
    );
    expect(got.total).toBeCloseTo(1, 6);
    expect(got.partial).toBe(true);
    const norate = got.entries.find((e) => e.model === 'norate');
    expect(norate?.priced).toBe(false);
    expect(norate?.tokensIn).toBe(1_000_000);
    expect(norate?.cost).toBe(0);
  });

  test('buckets turns recorded before per-model tracking', () => {
    const got = costBreakdown(
      [turn(undefined, '', 500, 500), turn('cheap', 'anthropic', 1_000_000, 0)],
      priced,
    );
    expect(got.unrecordedTurns).toBe(1);
    expect(got.partial).toBe(true);
    // The unpriceable turn must not silently inherit another model's rate.
    expect(got.total).toBeCloseTo(1, 6);
    expect(got.entries).toHaveLength(1);
  });

  test('ignores user turns and turns with no usage', () => {
    const user: Message = { role: 'user', content: 'hi' } as Message;
    const noUsage: Message = { role: 'assistant', content: '', model: 'cheap' } as Message;
    const got = costBreakdown([user, noUsage, turn('cheap', 'anthropic', 10, 10)], priced);
    expect(got.entries).toHaveLength(1);
    expect(got.unrecordedTurns).toBe(0);
  });

  test('zero-token turns do not create entries', () => {
    const got = costBreakdown([turn('cheap', 'anthropic', 0, 0)], priced);
    expect(got.entries).toHaveLength(0);
    expect(got.total).toBe(0);
  });

  test('orders entries by cost descending', () => {
    const got = costBreakdown(
      [turn('cheap', 'anthropic', 1_000_000, 0), turn('dear', 'anthropic', 1_000_000, 0)],
      priced,
    );
    expect(got.entries.map((e) => e.model)).toEqual(['dear', 'cheap']);
  });

  test('an empty chat is not partial', () => {
    const got = costBreakdown([], priced);
    expect(got).toMatchObject({ total: 0, partial: false, unrecordedTurns: 0 });
    expect(got.entries).toHaveLength(0);
  });

  test('a free-tier model counts as priced at zero, not as rate-unknown', () => {
    const free: Catalog = {
      openrouter: [{ id: 'free', contextTokens: 1000, costPerMTokIn: 0, costPerMTokOut: 0 }],
    };
    const got = costBreakdown([turn('free', 'openrouter', 1_000_000, 1_000_000)], free);
    expect(got.entries[0].priced).toBe(true);
    expect(got.total).toBe(0);
    expect(got.partial).toBe(false);
  });
});

describe('modelToRestore', () => {
  test('restores the remembered model once the catalog contains it', () => {
    // The reported bug: picked gpt-5.6-luna, reverted to gpt-5.4-nano.
    expect(
      modelToRestore('openai', ['gpt-5.4-nano', 'gpt-5.6-luna'], { openai: 'gpt-5.6-luna' }, 'gpt-5.4-nano'),
    ).toBe('gpt-5.6-luna');
  });

  test('waits rather than clobbering when the catalog is still the seed', () => {
    // Cold start: only the embedded seed is loaded and the remembered
    // model is a live-catalog entry. Returning '' leaves the selection
    // alone until the real catalog arrives.
    expect(
      modelToRestore('openai', ['gpt-5.4-nano', 'gpt-5.4-mini'], { openai: 'gpt-5.6-luna' }, 'gpt-5.4-nano'),
    ).toBe('');
  });

  test('does nothing when the remembered model is already selected', () => {
    expect(
      modelToRestore('openai', ['gpt-5.6-luna'], { openai: 'gpt-5.6-luna' }, 'gpt-5.6-luna'),
    ).toBe('');
  });

  test('does nothing without a memory for this provider', () => {
    expect(modelToRestore('openai', ['a', 'b'], { anthropic: 'x' }, 'a')).toBe('');
    expect(modelToRestore('openai', ['a', 'b'], undefined, 'a')).toBe('');
    expect(modelToRestore('openai', ['a', 'b'], {}, 'a')).toBe('');
  });

  test('does nothing before the catalog loads or without a provider', () => {
    expect(modelToRestore('openai', [], { openai: 'x' }, '')).toBe('');
    expect(modelToRestore('', ['a'], { openai: 'x' }, 'a')).toBe('');
  });

  test('is per provider', () => {
    const remembered = { openai: 'gpt-5.6-luna', anthropic: 'claude-opus-4-7' };
    expect(modelToRestore('anthropic', ['claude-haiku-4-5', 'claude-opus-4-7'], remembered, 'claude-haiku-4-5'))
      .toBe('claude-opus-4-7');
  });
});
