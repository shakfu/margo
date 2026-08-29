// Persona CRUD, plus the legacy agent accessors kept so pre-9.4
// records still deserialise.
//
// Split out of the former monolithic lib/store.ts. Import from
// `lib/store` (the index), not from these modules directly — the index
// is the module boundary consumers depend on.

import { get } from 'svelte/store';
import { chats, settings } from './stores';
import { uuid } from './persistence';
import { type Agent, type Persona } from './types';

export function visiblePersonas(personas: Persona[], activeWorkspaceId: string): Persona[] {
  return personas.filter(p => !p.workspaceId || p.workspaceId === activeWorkspaceId);
}

export function visibleAgents(agents: Agent[], activeWorkspaceId: string): Agent[] {
  return agents.filter(a => !a.workspaceId || a.workspaceId === activeWorkspaceId);
}

export function findPersona(personas: Persona[], id: string | undefined): Persona | undefined {
  if (!id) return undefined;
  return personas.find(p => p.id === id);
}

// upsertPersona writes a persona by id. Creating: pass an entry with a
// fresh id. Editing: pass an entry with the existing id. Builtins
// cannot be edited in place — the UI must duplicate them first.
export function upsertPersona(p: Persona) {
  settings.update(s => {
    const existing = s.personas.findIndex(x => x.id === p.id);
    const next = [...s.personas];
    if (existing >= 0) {
      if (next[existing].builtin) return s; // refuse to overwrite a builtin
      next[existing] = { ...p, builtin: false };
    } else {
      next.push({ ...p, builtin: false });
    }
    return { ...s, personas: next };
  });
}

export function deletePersona(id: string) {
  settings.update(s => {
    const target = s.personas.find(p => p.id === id);
    if (!target || target.builtin) return s;
    return { ...s, personas: s.personas.filter(p => p.id !== id) };
  });
}

// duplicatePersona returns the id of the new entry so the caller can
// open it in edit mode. Used to customise a builtin without losing it.
export function duplicatePersona(id: string): string | undefined {
  let newId: string | undefined;
  settings.update(s => {
    const src = s.personas.find(p => p.id === id);
    if (!src) return s;
    newId = uuid();
    const copy: Persona = {
      id: newId,
      name: `${src.name} (copy)`,
      description: src.description,
      systemPrompt: src.systemPrompt,
      builtin: false,
      // Inherit source's workspace scope. Duplicating a builtin (always
      // global) yields a global custom persona; the user can re-scope
      // via the Scope selector in the editor that opens immediately.
      workspaceId: src.workspaceId,
    };
    return { ...s, personas: [...s.personas, copy] };
  });
  return newId;
}

// setChatAgent binds an agent to the active chat. Setting an agent
// clears any persona on the same chat (mutually exclusive). Pass
// undefined to clear (Default mode).
export function setChatAgent(id: string, agentId: string | undefined) {
  chats.update(cs =>
    cs.map(c => (c.id === id ? { ...c, agentId, personaId: undefined, updatedAt: Date.now() } : c))
  );
}

export function findAgent(agents: Agent[], id: string | undefined): Agent | undefined {
  if (!id) return undefined;
  return agents.find(a => a.id === id);
}

// agentMissingTools returns the names of tools the agent declares but
// that aren't currently registered (e.g. agent references quarto_render
// but the user hasn't installed quarto). Empty result = agent is fully

export function agentMissingTools(agent: Agent, installed: string[]): string[] {
  const set = new Set(installed);
  return agent.tools.filter(t => !set.has(t));
}

export function upsertAgent(a: Agent) {
  if (!a.tools || a.tools.length === 0) return; // empty allowlist = persona, not agent
  settings.update(s => {
    const existing = s.agents.findIndex(x => x.id === a.id);
    const next = [...s.agents];
    if (existing >= 0) {
      if (next[existing].builtin) return s;
      next[existing] = { ...a, builtin: false };
    } else {
      next.push({ ...a, builtin: false });
    }
    return { ...s, agents: next };
  });
}

export function deleteAgent(id: string) {
  settings.update(s => {
    const target = s.agents.find(a => a.id === id);
    if (!target || target.builtin) return s;
    return { ...s, agents: s.agents.filter(a => a.id !== id) };
  });
}

export function duplicateAgent(id: string): string | undefined {
  let newId: string | undefined;
  settings.update(s => {
    const src = s.agents.find(a => a.id === id);
    if (!src) return s;
    newId = uuid();
    const copy: Agent = {
      id: newId,
      name: `${src.name} (copy)`,
      description: src.description,
      systemPrompt: src.systemPrompt,
      tools: [...src.tools],
      builtin: false,
      workspaceId: src.workspaceId,
    };
    return { ...s, agents: [...s.agents, copy] };
  });
  return newId;
}
