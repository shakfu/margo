import { writable, derived } from 'svelte/store';

export type Role = 'user' | 'assistant';

export interface Message {
  role: Role;
  content: string;
}

export interface Chat {
  id: string;
  title: string;
  messages: Message[];
  createdAt: number;
  updatedAt: number;
}

export interface Settings {
  provider: string;
  system: string;
  streaming: boolean;
  theme: 'light' | 'dark';
  showLeft: boolean;
  showRight: boolean;
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
    if (raw) return JSON.parse(raw) as Chat[];
  } catch (_) {}
  return [];
}

const defaults: Settings = {
  provider: '',
  system: '',
  streaming: true,
  theme: 'light',
  showLeft: true,
  showRight: true
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
    { id, title: 'New chat', messages: [], createdAt: now, updatedAt: now },
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
