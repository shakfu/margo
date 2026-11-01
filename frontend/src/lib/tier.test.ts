import { describe, test, expect, beforeEach, vi } from 'vitest';
import { tierAllows, MIN_TIER, type GatedControl } from './tier';
import { UI_TIERS } from './store/types';
import { computeSlashSuggestions, slashCommandsForTier } from './slash';

const visible = (tier: (typeof UI_TIERS)[number]) =>
  (Object.keys(MIN_TIER) as GatedControl[]).filter(c => tierAllows(tier, c)).sort();

describe('tierAllows', () => {
  test('novice sees no gated control', () => {
    expect(visible('novice')).toEqual([]);
  });
  test('standard sees sampling, thinking, trusted tools only', () => {
    expect(visible('standard')).toEqual(['sampling', 'thinking', 'trustedTools']);
  });
  test('expert sees every gated control', () => {
    expect(visible('expert')).toEqual(Object.keys(MIN_TIER).sort());
  });
});

describe('slashCommandsForTier', () => {
  const commands = (tier: (typeof UI_TIERS)[number]) => slashCommandsForTier(tier).map(c => c.command);
  test('hides runner commands below expert, keeps /agent', () => {
    for (const tier of ['novice', 'standard'] as const) {
      expect(commands(tier)).toContain('/agent');
      expect(commands(tier)).not.toContain('/agent-plan');
      expect(commands(tier)).not.toContain('/agent-workflow');
    }
  });
  test('expert keeps runner commands', () => {
    expect(commands('expert')).toContain('/agent-plan');
    expect(commands('expert')).toContain('/agent-workflow');
  });
});

describe('computeSlashSuggestions', () => {
  const texts = (s: string, tier: (typeof UI_TIERS)[number] = 'expert') =>
    computeSlashSuggestions(s, [{ name: 'Code Reviewer' }], tier).map(r => r.text);

  test('bare slash lists commands with a single leading slash', () => {
    expect(texts('/')).toContain('/agent ');
    expect(texts('/').every(t => !t.startsWith('//'))).toBe(true);
  });
  test('a typed prefix narrows the list, ignoring case', () => {
    expect(texts('/ag')).toEqual(['/agent ', '/agent-plan ', '/agent-workflow ']);
    expect(texts('/AG')).toEqual(['/agent ', '/agent-plan ', '/agent-workflow ']);
    expect(texts('/def')).toEqual(['/default ']);
  });
  test('tier gating applies to typed prefixes', () => {
    expect(texts('/ag', 'novice')).toEqual(['/agent ']);
  });
  test('persona names complete after /persona', () => {
    expect(texts('/persona co')).toEqual(['/persona code-reviewer ']);
  });
  test('no matches once a task follows the command', () => {
    expect(texts('/agent do it')).toEqual([]);
  });
  test('plain text and literal paths give no matches', () => {
    expect(texts('hello')).toEqual([]);
    expect(texts('/etc/passwd')).toEqual([]);
  });
});

describe('uiTier persistence', () => {
  const KEY = 'margo:settings:v1';
  async function load() {
    vi.resetModules();
    return (await import('./store')).loadSettings();
  }
  beforeEach(() => localStorage.clear());

  test('defaults to expert with empty storage', async () => {
    expect((await load()).uiTier).toBe('expert');
  });
  test('settings saved before uiTier existed load as expert', async () => {
    localStorage.setItem(KEY, JSON.stringify({ theme: 'dark' }));
    expect((await load()).uiTier).toBe('expert');
  });
  test('a saved tier is preserved', async () => {
    localStorage.setItem(KEY, JSON.stringify({ uiTier: 'novice' }));
    expect((await load()).uiTier).toBe('novice');
  });
  test('an unknown saved tier falls back to the default', async () => {
    localStorage.setItem(KEY, JSON.stringify({ uiTier: 'wizard' }));
    expect((await load()).uiTier).toBe('expert');
  });
});
