// localStorage keys, id generation, schema migrations, and the load
// paths for settings and per-workspace chats.
//
// Pure functions only: nothing here touches a Svelte store, so the
// migration logic stays unit-testable without instantiating the app's
// state. The side-effectful initialisation lives in ./stores.
//
// Split out of the former monolithic lib/store.ts. Import from
// `lib/store` (the index), not from these modules directly — the index
// is the module boundary consumers depend on.

import { BUILTIN_PERSONAS, BUILTIN_AGENTS, LEGACY_BUILTIN_AGENT_IDS } from './builtins';
import { DEFAULT_WORKSPACE_ID, type Agent, type Chat, type Persona, type Settings, type Workspace } from './types';

export const LEGACY_CHATS_KEY = 'margo:chats:v1';
export const SETTINGS_KEY = 'margo:settings:v1';

// chatsKey returns the localStorage key holding chats for `workspaceId`.
// Format chosen so the legacy single-workspace key (`margo:chats:v1`)
// can be unambiguously distinguished from per-workspace keys.
export function chatsKey(workspaceId: string): string {
  return `margo:chats:${workspaceId}:v1`;
}

export function uuid(): string {
  const c = (window as any).crypto;
  if (c?.randomUUID) return c.randomUUID();
  return `id-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}

// migrateLegacyChats moves chats from the pre-7.1.a single key into the
// Default workspace's key the first time we see them. Idempotent: if the
// new key already exists, the legacy key is left alone (the user has
// already been migrated and the workspaces feature has run at least
// once). Removes the legacy key on success so subsequent loads skip the
// branch entirely.
export function migrateLegacyChats(): void {
  try {
    const legacy = localStorage.getItem(LEGACY_CHATS_KEY);
    if (!legacy) return;
    const targetKey = chatsKey(DEFAULT_WORKSPACE_ID);
    if (!localStorage.getItem(targetKey)) {
      localStorage.setItem(targetKey, legacy);
    }
    localStorage.removeItem(LEGACY_CHATS_KEY);
  } catch (_) {}
}

// migrateAgentIdsToPersonaIds rewrites chats with stale §9.4-era
// bindings. Three cases:
//   1. The agentId pointed at a legacy builtin agent ("Quarto Author"
//      / "Time-aware assistant"). Those records were retired entirely
//      — there is no corresponding persona — so the field is just
//      cleared and the chat falls back to Default mode.
//   2. The agentId pointed at a user-created agent. The loadSettings
//      migration converts that agent into a persona of the same id,
//      so a straight copy `personaId = agentId` preserves the
//      binding.
//   3. The personaId points at a now-retired interim builtin (the
//      §9.4 interim builds added Quarto Author / Time-aware to
//      BUILTIN_PERSONAS briefly; the cleanup removed them). The
//      persona no longer exists, so the binding is cleared.
// Idempotent: chats whose fields are already clean are returned
// unchanged.
export function migrateAgentIdsToPersonaIds(chats: Chat[]): Chat[] {
  let changed = false;
  const out = chats.map(c => {
    let next = c;
    if (next.personaId && LEGACY_BUILTIN_AGENT_IDS.has(next.personaId)) {
      next = { ...next, personaId: undefined };
      changed = true;
    }
    if (next.agentId) {
      changed = true;
      if (LEGACY_BUILTIN_AGENT_IDS.has(next.agentId)) {
        next = { ...next, agentId: undefined };
      } else {
        next = { ...next, personaId: next.personaId ?? next.agentId, agentId: undefined };
      }
    }
    return next;
  });
  return changed ? out : chats;
}

export function loadChatsForWorkspace(workspaceId: string): Chat[] {
  try {
    const raw = localStorage.getItem(chatsKey(workspaceId));
    if (raw) {
      const parsed = JSON.parse(raw) as Chat[];
      // backfill new fields for chats persisted before tokens tracking
      const backfilled = parsed.map(c => ({
        ...c,
        tokensIn: c.tokensIn ?? 0,
        tokensOut: c.tokensOut ?? 0,
      }));
      return migrateAgentIdsToPersonaIds(backfilled);
    }
  } catch (_) {}
  return [];
}

export const defaults: Settings = {
  provider: '',
  model: '',
  system: '',
  streaming: true,
  theme: 'light',
  showLeft: true,
  showRight: true,
  maxTokens: 4096,
  temperature: null,
  topP: null,
  stopSequences: [],
  thinkEnabled: false,
  thinkBudget: 4096,
  agentMode: false,
  autoApproveTools: [],
  lastModelByProvider: {},
  personas: BUILTIN_PERSONAS,
  agents: BUILTIN_AGENTS,
  workspaces: [{
    id: DEFAULT_WORKSPACE_ID,
    name: 'Default',
    createdAt: 0,
    updatedAt: 0,
  }],
  activeWorkspaceId: DEFAULT_WORKSPACE_ID,
};

// Exported for store.test.ts so the migration paths (legacy agents →
// personas, builtin re-assertion, workspace invariants, default-
// persona dangling-id cleanup) have a unit-test surface. Not called
// from frontend code outside this module; do not rely on this as a
// public API.
export function loadSettings(): Settings {
  try {
    const raw = localStorage.getItem(SETTINGS_KEY);
    if (raw) {
      const merged = { ...defaults, ...JSON.parse(raw) };
      // Merge builtin personas into the persisted list, preserving custom
      // entries and any user edits to builtins. Builtins are matched by id
      // and re-asserted with the latest ship version of their fields, so a
      // user can't permanently delete a builtin (it reappears on next load
      // if removed by hand). Customising a builtin requires duplicating it
      // in the UI — that produces a non-builtin entry with a fresh id.
      const userPersonas: Persona[] = Array.isArray(merged.personas) ? merged.personas : [];
      const builtinPersonaIds = new Set(BUILTIN_PERSONAS.map(p => p.id));
      let customPersonas = userPersonas
        .filter(p => !builtinPersonaIds.has(p.id))
        // §9.4 interim builds briefly placed "Quarto Author" and
        // "Time-aware assistant" into BUILTIN_PERSONAS. Removing them
        // from the catalog later left those ids in localStorage as
        // "custom" personas (since they no longer matched a builtin
        // id). Drop them on load — same idempotent migration logic as
        // the LEGACY_BUILTIN_AGENT_IDS check below.
        .filter(p => !LEGACY_BUILTIN_AGENT_IDS.has(p.id));

      // §9.4 migration: user-created agents become personas. Built-in
      // agent ids match the new built-in persona ids, so built-ins
      // drop out via the builtin-id filter above; only non-builtin
      // user agents need translation. We preserve the agent's id so
      // any chat that referenced agentId can keep using the same id
      // as personaId. The tool allowlist is surfaced as a hint
      // appended to the system prompt — the new model is "enable
      // tools in the Tools tab and use this persona."
      const userAgents: Agent[] = Array.isArray(merged.agents) ? merged.agents : [];
      const personaIdsAlready = new Set<string>(customPersonas.map(p => p.id));
      builtinPersonaIds.forEach(id => personaIdsAlready.add(id));
      const migratedFromAgents: Persona[] = userAgents
        .filter(a => !a.builtin)
        .filter(a => !personaIdsAlready.has(a.id))
        .map(a => ({
          id: a.id,
          name: a.name,
          description: a.description,
          systemPrompt: a.tools && a.tools.length > 0
            ? `${a.systemPrompt}\n\n(This role pairs with the ${a.tools.map(t => '`' + t + '`').join(', ')} tool${a.tools.length === 1 ? '' : 's'} — make sure they\'re enabled in Settings → Tools, and invoke via \`/agent <task>\`.)`
            : a.systemPrompt,
          builtin: false,
          workspaceId: a.workspaceId,
        }));
      customPersonas = [...customPersonas, ...migratedFromAgents];
      merged.personas = [...BUILTIN_PERSONAS, ...customPersonas];
      // Drain agents — the type still exists for legacy
      // deserialisation but the array is always empty after migration.
      merged.agents = BUILTIN_AGENTS;
      // Clear any workspace.defaultPersonaId that points at a
      // persona we no longer ship. Same idempotent migration shape
      // as the chat.personaId clean-up in
      // migrateAgentIdsToPersonaIds.
      const validPersonaIds = new Set(merged.personas.map((p: Persona) => p.id));
      if (Array.isArray(merged.workspaces)) {
        merged.workspaces = merged.workspaces.map((w: Workspace) => {
          if (w.defaultPersonaId && !validPersonaIds.has(w.defaultPersonaId)) {
            return { ...w, defaultPersonaId: undefined };
          }
          return w;
        });
      }
      // Workspace invariants: at least one workspace; Default always present;
      // activeWorkspaceId points at a real entry. Re-asserting Default on
      // load makes "user deleted Default by editing storage" non-fatal.
      const userWorkspaces: Workspace[] = Array.isArray(merged.workspaces) ? merged.workspaces : [];
      const hasDefault = userWorkspaces.some(w => w.id === DEFAULT_WORKSPACE_ID);
      const workspaces = hasDefault
        ? userWorkspaces
        : [defaults.workspaces[0], ...userWorkspaces];
      merged.workspaces = workspaces;
      if (!workspaces.some(w => w.id === merged.activeWorkspaceId)) {
        merged.activeWorkspaceId = DEFAULT_WORKSPACE_ID;
      }
      // Seed lastModelByProvider from the single `model` field so an
      // upgrading user keeps the model they were last using.
      if (!merged.lastModelByProvider || typeof merged.lastModelByProvider !== 'object') {
        merged.lastModelByProvider = {};
      }
      if (merged.provider && merged.model && !merged.lastModelByProvider[merged.provider]) {
        merged.lastModelByProvider[merged.provider] = merged.model;
      }
      return merged;
    }
  } catch (_) {}
  return { ...defaults };
}
