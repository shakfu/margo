// Tests for store.ts migration logic. The store has been touched in
// nearly every recent CHANGELOG entry; the migration paths run at
// every app start and any regression silently corrupts user data.
// This file is the regression net for the cases REVIEW §7.5 called
// out as the highest-value first frontend tests.
//
// Pattern: seed localStorage with a stale persisted shape, dynamically
// re-import store.ts so loadSettings runs against that seed, assert
// the migrated output. vi.resetModules() between tests ensures each
// case sees a fresh module state.

import { describe, test, expect, beforeEach, vi } from 'vitest';

const SETTINGS_KEY = 'margo:settings:v1';
const DEFAULT_WORKSPACE_ID = 'default';

async function freshStore() {
  // Re-import so the module-level `const initialSettings = loadSettings()`
  // re-runs against the current localStorage.
  vi.resetModules();
  return await import('./store');
}

beforeEach(() => {
  localStorage.clear();
});

describe('loadSettings — empty / default cases', () => {
  test('returns defaults when localStorage is empty', async () => {
    const { loadSettings, DEFAULT_WORKSPACE_ID: defaultWsId } = await freshStore();
    const s = loadSettings();
    expect(s.activeWorkspaceId).toBe(defaultWsId);
    expect(s.workspaces.length).toBeGreaterThan(0);
    expect(s.workspaces.some(w => w.id === defaultWsId)).toBe(true);
    // Builtins are always present in the personas array, even from a
    // cold start (defaults seed them).
    expect(s.personas.length).toBeGreaterThan(0);
    expect(s.personas.every(p => p.id)).toBe(true);
  });

  test('returns defaults when localStorage holds malformed JSON', async () => {
    localStorage.setItem(SETTINGS_KEY, '{not json');
    const { loadSettings } = await freshStore();
    const s = loadSettings();
    expect(s.activeWorkspaceId).toBe(DEFAULT_WORKSPACE_ID);
    expect(Array.isArray(s.workspaces)).toBe(true);
  });
});

describe('loadSettings — workspace invariants', () => {
  test('Default workspace is re-asserted when missing', async () => {
    localStorage.setItem(SETTINGS_KEY, JSON.stringify({
      // User somehow lost the Default workspace; loadSettings must
      // re-inject it so the rest of the app doesn't see an empty list.
      workspaces: [{ id: 'project-a', name: 'Project A', createdAt: 1, updatedAt: 1 }],
      activeWorkspaceId: 'project-a',
    }));
    const { loadSettings } = await freshStore();
    const s = loadSettings();
    expect(s.workspaces.some(w => w.id === DEFAULT_WORKSPACE_ID)).toBe(true);
    // The user's chosen workspace stays selected — re-asserting Default
    // does not yank the active selection.
    expect(s.activeWorkspaceId).toBe('project-a');
  });

  test('invalid activeWorkspaceId falls back to Default', async () => {
    localStorage.setItem(SETTINGS_KEY, JSON.stringify({
      workspaces: [{ id: DEFAULT_WORKSPACE_ID, name: 'Default', createdAt: 0, updatedAt: 0 }],
      activeWorkspaceId: 'deleted-workspace-id',
    }));
    const { loadSettings } = await freshStore();
    const s = loadSettings();
    expect(s.activeWorkspaceId).toBe(DEFAULT_WORKSPACE_ID);
  });

  test('workspace.defaultPersonaId pointing at a missing persona is cleared', async () => {
    localStorage.setItem(SETTINGS_KEY, JSON.stringify({
      workspaces: [
        { id: DEFAULT_WORKSPACE_ID, name: 'Default', createdAt: 0, updatedAt: 0 },
        { id: 'ws-x', name: 'X', createdAt: 1, updatedAt: 1, defaultPersonaId: 'ghost-persona' },
      ],
      activeWorkspaceId: DEFAULT_WORKSPACE_ID,
    }));
    const { loadSettings } = await freshStore();
    const s = loadSettings();
    const wsX = s.workspaces.find(w => w.id === 'ws-x');
    expect(wsX).toBeDefined();
    // The dangling id is removed but the workspace row survives — this
    // is the idempotent-cleanup pattern called out in the inline
    // comments at the load path.
    expect(wsX!.defaultPersonaId).toBeUndefined();
  });
});

describe('loadSettings — legacy agent → persona migration (§9.4)', () => {
  test('user-created non-builtin agents become custom personas with a tool hint appended', async () => {
    localStorage.setItem(SETTINGS_KEY, JSON.stringify({
      workspaces: [{ id: DEFAULT_WORKSPACE_ID, name: 'Default', createdAt: 0, updatedAt: 0 }],
      activeWorkspaceId: DEFAULT_WORKSPACE_ID,
      personas: [],
      agents: [{
        id: 'agent-custom',
        name: 'Researcher',
        description: 'Reads the web',
        systemPrompt: 'You are a researcher.',
        tools: ['web_fetch'],
        autoApprove: [],
        workspaceId: DEFAULT_WORKSPACE_ID,
        builtin: false,
      }],
    }));
    const { loadSettings } = await freshStore();
    const s = loadSettings();
    // The migrated persona keeps the agent's id so any chat referencing
    // the old agentId can transparently use it as personaId.
    const migrated = s.personas.find(p => p.id === 'agent-custom');
    expect(migrated).toBeDefined();
    expect(migrated!.name).toBe('Researcher');
    expect(migrated!.builtin).toBe(false);
    // Tool hint surfaces the old allowlist so the user knows to enable
    // those tools in Settings → Tools.
    expect(migrated!.systemPrompt).toContain('web_fetch');
    expect(migrated!.systemPrompt).toContain('/agent');
    // The agents array is drained on migration; nothing user-defined
    // should survive there.
    expect(s.agents.every(a => a.builtin)).toBe(true);
  });

  test('builtin personas are re-asserted with current ship versions (user cannot delete them)', async () => {
    localStorage.setItem(SETTINGS_KEY, JSON.stringify({
      workspaces: [{ id: DEFAULT_WORKSPACE_ID, name: 'Default', createdAt: 0, updatedAt: 0 }],
      activeWorkspaceId: DEFAULT_WORKSPACE_ID,
      personas: [],
      agents: [],
    }));
    const { loadSettings, BUILTIN_PERSONAS } = await freshStore();
    const s = loadSettings();
    // Every shipped builtin must appear in the returned personas list,
    // even though the persisted shape had personas: [].
    for (const b of BUILTIN_PERSONAS) {
      expect(s.personas.some(p => p.id === b.id && p.builtin)).toBe(true);
    }
  });

  test('legacy-but-removed builtin agent ids are dropped on load (idempotent)', async () => {
    // §9.4 interim builds briefly placed "Quarto Author" and
    // "Time-aware assistant" into BUILTIN_PERSONAS. Removing them
    // later left their ids (now hand-collected in
    // LEGACY_BUILTIN_AGENT_IDS) in localStorage as "custom" personas.
    // Loading must drop them rather than resurrecting them.
    localStorage.setItem(SETTINGS_KEY, JSON.stringify({
      workspaces: [{ id: DEFAULT_WORKSPACE_ID, name: 'Default', createdAt: 0, updatedAt: 0 }],
      activeWorkspaceId: DEFAULT_WORKSPACE_ID,
      personas: [
        { id: 'builtin-quarto-author', name: 'Quarto Author (stale)', systemPrompt: 'old', builtin: false },
        { id: 'builtin-time-aware',    name: 'Time-aware (stale)',    systemPrompt: 'old', builtin: false },
      ],
      agents: [],
    }));
    const { loadSettings } = await freshStore();
    const s = loadSettings();
    expect(s.personas.some(p => p.id === 'builtin-quarto-author')).toBe(false);
    expect(s.personas.some(p => p.id === 'builtin-time-aware')).toBe(false);
  });
});
