import { writable, derived } from 'svelte/store';

export type Role = 'user' | 'assistant';

export interface Usage {
  inputTokens: number;
  outputTokens: number;
  firstTokenMs: number;
  totalMs: number;
}

export type StepKind = 'tool_call' | 'tool_result';

export interface AgentStep {
  kind: StepKind;
  name: string;
  arguments?: string;
  result?: string;
  isError?: boolean;
}

export interface Message {
  role: Role;
  content: string;
  thinking?: string;
  usage?: Usage;
  steps?: AgentStep[];
}

export interface Chat {
  id: string;
  title: string;
  messages: Message[];
  createdAt: number;
  updatedAt: number;
  tokensIn: number;
  tokensOut: number;
}

export interface Settings {
  provider: string;
  model: string;
  system: string;
  streaming: boolean;
  theme: 'light' | 'dark';
  showLeft: boolean;
  showRight: boolean;
  maxTokens: number;
  temperature: number | null;
  topP: number | null;
  stopSequences: string[];
  thinkEnabled: boolean;
  thinkBudget: number;
  agentMode: boolean;
}

// Approximate context window per model. Used by the context-usage ring.
// Numbers are conservative; intent is "is this conversation about to overflow",
// not exact accounting.
export const CONTEXT_WINDOWS: Record<string, number> = {
  'claude-haiku-4-5': 200_000,
  'claude-sonnet-4-6': 200_000,
  'claude-opus-4-7': 1_000_000,
  'gpt-5.5': 400_000,
  'gpt-5.5-pro': 400_000,
  'gpt-5.4': 400_000,
  'gpt-5.4-mini': 400_000,
  'gpt-5.4-nano': 400_000,
  'gpt-5.4-pro': 400_000,
};

export function contextWindowFor(model: string): number {
  return CONTEXT_WINDOWS[model] ?? 128_000;
}

const CHATS_KEY = 'margo:chats:v1';
const SETTINGS_KEY = 'margo:settings:v1';

function uuid(): string {
  const c = (window as any).crypto;
  if (c?.randomUUID) return c.randomUUID();
  return `id-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}

function loadChats(): Chat[] {
  try {
    const raw = localStorage.getItem(CHATS_KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as Chat[];
      // backfill new fields for chats persisted before tokens tracking
      return parsed.map(c => ({
        ...c,
        tokensIn: c.tokensIn ?? 0,
        tokensOut: c.tokensOut ?? 0,
      }));
    }
  } catch (_) {}
  return [];
}

const defaults: Settings = {
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
};

function loadSettings(): Settings {
  try {
    const raw = localStorage.getItem(SETTINGS_KEY);
    if (raw) return { ...defaults, ...JSON.parse(raw) };
  } catch (_) {}
  return { ...defaults };
}

export const chats = writable<Chat[]>(loadChats());
chats.subscribe(cs => {
  try { localStorage.setItem(CHATS_KEY, JSON.stringify(cs)); } catch (_) {}
});

export const activeChatId = writable<string>('');

export const activeChat = derived(
  [chats, activeChatId],
  ([$chats, $id]) => $chats.find(c => c.id === $id) ?? null
);

export const settings = writable<Settings>(loadSettings());
settings.subscribe(s => {
  try { localStorage.setItem(SETTINGS_KEY, JSON.stringify(s)); } catch (_) {}
});

export function newChat(): string {
  const id = uuid();
  const now = Date.now();
  chats.update(cs => [
    {
      id,
      title: 'New chat',
      messages: [],
      createdAt: now,
      updatedAt: now,
      tokensIn: 0,
      tokensOut: 0,
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
