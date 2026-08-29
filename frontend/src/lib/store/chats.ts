// Chat CRUD and the streaming-message mutations.
//
// Split out of the former monolithic lib/store.ts. Import from
// `lib/store` (the index), not from these modules directly — the index
// is the module boundary consumers depend on.

import { get } from 'svelte/store';
import { chats, activeChatId, settings } from './stores';
import { uuid } from './persistence';
import { type AgentStep, type Message, type RetrievalHit, type Usage } from './types';

export function newChat(): string {
  const id = uuid();
  const now = Date.now();
  // Seed the new chat with the active workspace's default persona,
  // if one is set. Per-chat `/persona <slug>` later overrides this.
  const s = get(settings);
  const ws = s.workspaces.find(w => w.id === s.activeWorkspaceId);
  const seedPersonaId = ws?.defaultPersonaId;
  chats.update(cs => [
    {
      id,
      title: 'New chat',
      messages: [],
      createdAt: now,
      updatedAt: now,
      tokensIn: 0,
      tokensOut: 0,
      personaId: seedPersonaId,
    },
    ...cs
  ]);
  activeChatId.set(id);
  return id;
}

export function deleteChat(id: string) {
  let nextActive = '';
  chats.update(cs => {
    const filtered = cs.filter(c => c.id !== id);
    if (filtered.length > 0) nextActive = filtered[0].id;
    return filtered;
  });
  activeChatId.update(curr => (curr === id ? nextActive : curr));
}

export function renameChat(id: string, title: string) {
  chats.update(cs =>
    cs.map(c => (c.id === id ? { ...c, title, updatedAt: Date.now() } : c))
  );
}

// setChatPersona binds a persona to the active chat. Pass undefined to
// clear (Default mode). Setting a persona clears any agent (mutually
// exclusive). Bumps updatedAt so the chat sorts to the top.
export function setChatPersona(id: string, personaId: string | undefined) {
  chats.update(cs =>
    cs.map(c => (c.id === id ? { ...c, personaId, agentId: undefined, updatedAt: Date.now() } : c))
  );
}

// visiblePersonas filters the persona library down to those that should
// appear in the picker for the given active workspace: anything global
// (workspaceId undefined) plus anything scoped to the active workspace.

// available; non-empty = agent should be greyed out / disabled in the
// picker with the missing names surfaced.

export function appendMessage(id: string, msg: Message) {
  chats.update(cs =>
    cs.map(c => {
      if (c.id !== id) return c;
      const messages = [...c.messages, msg];
      let title = c.title;
      if (c.messages.length === 0 && msg.role === 'user') {
        title = msg.content.slice(0, 50).replace(/\s+/g, ' ').trim() || 'New chat';
      }
      return { ...c, messages, title, updatedAt: Date.now() };
    })
  );
}

export function appendToLast(id: string, delta: string) {
  chats.update(cs =>
    cs.map(c => {
      if (c.id !== id || c.messages.length === 0) return c;
      const messages = [...c.messages];
      const last = messages[messages.length - 1];
      messages[messages.length - 1] = { ...last, content: last.content + delta };
      return { ...c, messages, updatedAt: Date.now() };
    })
  );
}

export function appendThinkingToLast(id: string, delta: string) {
  chats.update(cs =>
    cs.map(c => {
      if (c.id !== id || c.messages.length === 0) return c;
      const messages = [...c.messages];
      const last = messages[messages.length - 1];
      messages[messages.length - 1] = {
        ...last,
        thinking: (last.thinking ?? '') + delta,
      };
      return { ...c, messages, updatedAt: Date.now() };
    })
  );
}

export function appendStepToLast(id: string, step: AgentStep) {
  chats.update(cs =>
    cs.map(c => {
      if (c.id !== id || c.messages.length === 0) return c;
      const messages = [...c.messages];
      const last = messages[messages.length - 1];
      const steps = [...(last.steps ?? []), step];
      messages[messages.length - 1] = { ...last, steps };
      return { ...c, messages, updatedAt: Date.now() };
    })
  );
}

// resolvePermissionStep finds a pending permission step by its permissionId
// (across the active chat's most-recent assistant message) and stamps it
// with the user's decision. Clears the id so the UI hides the buttons.
export function resolvePermissionStep(
  id: string,
  permissionId: string,
  status: 'approved' | 'denied',
) {
  chats.update(cs =>
    cs.map(c => {
      if (c.id !== id || c.messages.length === 0) return c;
      const messages = [...c.messages];
      const last = messages[messages.length - 1];
      const steps = [...(last.steps ?? [])];
      for (let i = steps.length - 1; i >= 0; i--) {
        const s = steps[i];
        if (s.kind === 'permission' && s.permissionId === permissionId) {
          steps[i] = { ...s, permissionStatus: status, permissionId: undefined };
          break;
        }
      }
      messages[messages.length - 1] = { ...last, steps };
      return { ...c, messages, updatedAt: Date.now() };
    })
  );
}

// appendStepStream finds the most recent tool_call step for `name` that
// is still streaming (no result yet) and appends a chunk to its `stream`
// buffer. No-op if no matching open call exists — this guards against
// out-of-order delivery on Wails' event channel.
export function appendStepStream(id: string, name: string, chunk: string) {
  if (!chunk) return;
  chats.update(cs =>
    cs.map(c => {
      if (c.id !== id || c.messages.length === 0) return c;
      const messages = [...c.messages];
      const last = messages[messages.length - 1];
      const steps = [...(last.steps ?? [])];
      for (let i = steps.length - 1; i >= 0; i--) {
        const s = steps[i];
        if (s.kind === 'tool_call' && s.name === name && s.result === undefined) {
          steps[i] = { ...s, stream: (s.stream ?? '') + chunk };
          break;
        }
      }
      messages[messages.length - 1] = { ...last, steps };
      return { ...c, messages, updatedAt: Date.now() };
    })
  );
}

// setStepHits attaches a structured retrieval payload to the most recent
// tool_call step for `name`. Renderer logic in App.svelte uses the
// presence of `hits` to switch the step card from raw-text to hit-card
// layout. No-op if no matching open call exists.
export function setStepHits(id: string, name: string, hits: RetrievalHit[]) {
  if (!hits || hits.length === 0) return;
  chats.update(cs =>
    cs.map(c => {
      if (c.id !== id || c.messages.length === 0) return c;
      const messages = [...c.messages];
      const last = messages[messages.length - 1];
      const steps = [...(last.steps ?? [])];
      for (let i = steps.length - 1; i >= 0; i--) {
        const s = steps[i];
        if (s.kind === 'tool_call' && s.name === name && s.result === undefined) {
          steps[i] = { ...s, hits };
          break;
        }
      }
      messages[messages.length - 1] = { ...last, steps };
      return { ...c, messages, updatedAt: Date.now() };
    })
  );
}

// updateLastStepResult finds the most recent tool_call step for `name` that
// is missing a paired tool_result and attaches the result. Falls back to
// appending a fresh tool_result step if no matching call is found.
export function updateLastStepResult(id: string, name: string, result: string, isError: boolean) {
  chats.update(cs =>
    cs.map(c => {
      if (c.id !== id || c.messages.length === 0) return c;
      const messages = [...c.messages];
      const last = messages[messages.length - 1];
      const steps = [...(last.steps ?? [])];
      let merged = false;
      for (let i = steps.length - 1; i >= 0; i--) {
        const s = steps[i];
        if (s.kind === 'tool_call' && s.name === name && s.result === undefined) {
          steps[i] = { ...s, result, isError };
          merged = true;
          break;
        }
      }
      if (!merged) {
        steps.push({ kind: 'tool_result', name, result, isError });
      }
      messages[messages.length - 1] = { ...last, steps };
      return { ...c, messages, updatedAt: Date.now() };
    })
  );
}

export function setLastUsage(id: string, usage: Usage) {
  chats.update(cs =>
    cs.map(c => {
      if (c.id !== id || c.messages.length === 0) return c;
      const messages = [...c.messages];
      const last = messages[messages.length - 1];
      messages[messages.length - 1] = { ...last, usage };
      return {
        ...c,
        messages,
        tokensIn: c.tokensIn + usage.inputTokens,
        tokensOut: c.tokensOut + usage.outputTokens,
        updatedAt: Date.now(),
      };
    })
  );
}
