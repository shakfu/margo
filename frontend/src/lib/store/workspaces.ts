// Workspace CRUD, the per-workspace override writers, and the
// per-provider model memory.
//
// setEffectiveOverride lives here rather than in ./stores because it
// dispatches to setWorkspaceOverride; putting it beside the store
// singletons would make ./stores and this module mutually dependent.
//
// Split out of the former monolithic lib/store.ts. Import from
// `lib/store` (the index), not from these modules directly — the index
// is the module boundary consumers depend on.

import { get } from 'svelte/store';
import { chats, settings, sessionOverrides, activeChatId, activeWorkspaceIdNow, withSuppressedChatsWrite } from './stores';
import { chatsKey, loadChatsForWorkspace, uuid } from './persistence';
import { DEFAULT_WORKSPACE_ID, type Agent, type Persona, type Workspace, type WorkspaceOverrides, type WorkspaceTemplate } from './types';
import { WORKSPACE_TEMPLATES } from './builtins';

// setEffectiveOverride writes an override for whichever workspace is
// active, routing to the session-scoped store for Default (whose
// overrides are deliberately not persisted) and to the persisted
// per-workspace table for everything else. Used by the workspace-mode
// SettingsPanel.
export function setEffectiveOverride<K extends keyof WorkspaceOverrides>(
  key: K,
  value: WorkspaceOverrides[K] | undefined,
) {
  const wsId = activeWorkspaceIdNow();
  if (wsId === DEFAULT_WORKSPACE_ID) {
    sessionOverrides.update(o => {
      const next = { ...o };
      if (value === undefined) delete next[key];
      else next[key] = value;
      return next;
    });
    return;
  }
  setWorkspaceOverride(wsId, key, value);
}

// ---- Workspace CRUD (7.1.a) ----

// addWorkspace creates and returns the new workspace id. Does not
// switch to it; callers decide whether to setActiveWorkspace afterwards.
export function addWorkspace(name: string, dir?: string): string {
  const id = uuid();
  const now = Date.now();
  const ws: Workspace = { id, name: name.trim() || "Untitled", dir, createdAt: now, updatedAt: now };
  settings.update(s => ({ ...s, workspaces: [...s.workspaces, ws] }));
  return id;
}

// createWorkspaceFromTemplate builds a workspace pre-populated from
// `template`: scoped personas/agents installed, overrides applied,
// optional dir attached. Returns the new workspace id. (7.1.f)
//
// `name` overrides the template's default name when non-empty. All
// inserted personas/agents get fresh ids so re-using the same
// template across workspaces produces independent copies.
export function createWorkspaceFromTemplate(
  template: WorkspaceTemplate,
  name?: string,
  dir?: string,
): string {
  const wsId = uuid();
  const now = Date.now();
  const ws: Workspace = {
    id: wsId,
    name: (name ?? '').trim() || template.name,
    dir,
    createdAt: now,
    updatedAt: now,
    overrides: template.overrides ? { ...template.overrides } : undefined,
  };

  const newPersonas: Persona[] = (template.personas ?? []).map(p => ({
    ...p,
    id: uuid(),
    builtin: false,
    workspaceId: wsId,
  }));
  const newAgents: Agent[] = (template.agents ?? []).map(a => ({
    ...a,
    id: uuid(),
    builtin: false,
    workspaceId: wsId,
    tools: [...a.tools],
  }));

  settings.update(s => ({
    ...s,
    workspaces: [...s.workspaces, ws],
    personas: [...s.personas, ...newPersonas],
    agents: [...s.agents, ...newAgents],
  }));
  return wsId;
}

export function renameWorkspace(id: string, name: string) {
  const trimmed = name.trim();
  if (!trimmed) return;
  settings.update(s => ({
    ...s,
    workspaces: s.workspaces.map(w => w.id === id ? { ...w, name: trimmed, updatedAt: Date.now() } : w),
  }));
}

// setWorkspaceToolEnabled flips a single tool in a workspace's
// enabledTools list (§9.3). When a workspace has never had its
// palette narrowed (enabledTools === undefined), the first call
// seeds the list from `available` so toggling one tool off doesn't
// implicitly turn everything else off. Subsequent calls add to or
// remove from the explicit list.
export function setWorkspaceToolEnabled(
  workspaceId: string,
  toolName: string,
  enabled: boolean,
  available: string[],
) {
  settings.update(s => ({
    ...s,
    workspaces: s.workspaces.map(w => {
      if (w.id !== workspaceId) return w;
      let current = w.enabledTools;
      if (current === undefined) {
        // Seed from the registered tools so an explicit toggle never
        // silently disables every other tool by side-effect.
        current = [...available];
      }
      const has = current.includes(toolName);
      let next: string[];
      if (enabled && !has) next = [...current, toolName];
      else if (!enabled && has) next = current.filter(t => t !== toolName);
      else next = current;
      return { ...w, enabledTools: next, updatedAt: Date.now() };
    }),
  }));
}

// isToolEnabledForWorkspace is the single source of truth the §9.3
// resolution (and the Tools tab UI) reads. Undefined enabledTools is
// the migration-safe "all enabled" baseline.
export function isToolEnabledForWorkspace(w: Workspace | undefined, toolName: string): boolean {
  if (!w || w.enabledTools === undefined) return true;
  return w.enabledTools.includes(toolName);
}

// setWorkspaceDefaultPersona binds (or clears, with undefined) the
// persona id that new chats in this workspace start with. Existing
// chats are unaffected — per-chat overrides via `/persona <slug>`
// stay sticky to the chat that issued them.
export function setWorkspaceDefaultPersona(workspaceId: string, personaId: string | undefined) {
  settings.update(s => ({
    ...s,
    workspaces: s.workspaces.map(w => {
      if (w.id !== workspaceId) return w;
      return { ...w, defaultPersonaId: personaId, updatedAt: Date.now() };
    }),
  }));
}

// setWorkspaceDir attaches (or clears, with undefined) a directory path
// to a workspace. The path is stored but not consumed in 7.1.a — later
// slices (RAG, file context) read it.
export function setWorkspaceDir(id: string, dir: string | undefined) {
  settings.update(s => ({
    ...s,
    workspaces: s.workspaces.map(w => w.id === id ? { ...w, dir: dir || undefined, updatedAt: Date.now() } : w),
  }));
}

// deleteWorkspace removes a workspace and its persisted chats. Refuses
// to delete the Default workspace (id-pinned to keep migration logic
// simple). If the deleted workspace was active, swaps to Default.
export function deleteWorkspace(id: string) {
  if (id === DEFAULT_WORKSPACE_ID) return;
  let wasActive = false;
  // Cascade: a workspace owns its scoped personas/agents. Globals
  // (workspaceId undefined) and builtins are untouched. Any chat in
  // the deleted workspace would also be wiped, but those live in a
  // separate localStorage key removed below.
  settings.update(s => {
    wasActive = s.activeWorkspaceId === id;
    return {
      ...s,
      workspaces: s.workspaces.filter(w => w.id !== id),
      personas: s.personas.filter(p => p.workspaceId !== id),
      agents: s.agents.filter(a => a.workspaceId !== id),
    };
  });
  try { localStorage.removeItem(chatsKey(id)); } catch (_) {}
  if (wasActive) setActiveWorkspace(DEFAULT_WORKSPACE_ID);
}

// setWorkspaceOverride sets a single override key on the given workspace.
// Pass undefined to clear that key (the inverse is clearWorkspaceOverride).
// (7.1.c)
export function setWorkspaceOverride<K extends keyof WorkspaceOverrides>(
  workspaceId: string,
  key: K,
  value: WorkspaceOverrides[K] | undefined,
) {
  settings.update(s => ({
    ...s,
    workspaces: s.workspaces.map(w => {
      if (w.id !== workspaceId) return w;
      const overrides: WorkspaceOverrides = { ...(w.overrides ?? {}) };
      if (value === undefined) {
        delete overrides[key];
      } else {
        overrides[key] = value;
      }
      const next: Workspace = { ...w, updatedAt: Date.now() };
      // Drop the overrides field entirely when empty so reads can
      // short-circuit on falsy.
      if (Object.keys(overrides).length === 0) {
        delete next.overrides;
      } else {
        next.overrides = overrides;
      }
      return next;
    }),
  }));
}

export function clearWorkspaceOverride<K extends keyof WorkspaceOverrides>(
  workspaceId: string,
  key: K,
) {
  setWorkspaceOverride(workspaceId, key, undefined);
}

export function clearAllWorkspaceOverrides(workspaceId: string) {
  settings.update(s => ({
    ...s,
    workspaces: s.workspaces.map(w => {
      if (w.id !== workspaceId) return w;
      const next: Workspace = { ...w, updatedAt: Date.now() };
      delete next.overrides;
      return next;
    }),
  }));
}

// setActiveWorkspace swaps the active workspace and reloads chats from
// its dedicated key. No-op if the id is already active or unknown.
// Clears activeChatId since chat ids do not span workspaces.
export function setActiveWorkspace(id: string) {
  if (id === activeWorkspaceIdNow()) return;
  // Validate against the persisted list before swapping. Also bump
  // the activated workspace's updatedAt so the picker can sort by
  // recency (7.1.e).
  let valid = false;
  const now = Date.now();
  settings.update(s => {
    if (!s.workspaces.some(w => w.id === id)) return s;
    valid = true;
    return {
      ...s,
      activeWorkspaceId: id,
      workspaces: s.workspaces.map(w => w.id === id ? { ...w, updatedAt: now } : w),
    };
  });
  if (!valid) return;
  // settings subscription has already updated currentActiveWorkspaceId.
  const next = loadChatsForWorkspace(id);
  withSuppressedChatsWrite(() => chats.set(next));
  activeChatId.set(next[0]?.id ?? "");
}

// rememberModel records the model as this provider's last choice.
// Called on every explicit model pick, not on the reactive fallback
// that snaps to the catalog default — otherwise a provider whose
// remembered model has been retired would immediately overwrite the
// memory with the fallback.
export function rememberModel(provider: string, model: string): void {
  if (!provider || !model) return;
  settings.update(s => ({
    ...s,
    lastModelByProvider: { ...s.lastModelByProvider, [provider]: model },
  }));
}

// modelForProvider returns the model to select when switching to
// `provider`: the remembered one when it is still in the catalog,
// otherwise the catalog's first entry, otherwise empty.
export function modelForProvider(
  provider: string,
  available: string[],
  remembered: Record<string, string>,
): string {
  const last = remembered?.[provider];
  if (last && available.includes(last)) return last;
  return available[0] ?? '';
}
