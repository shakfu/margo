import { writable, derived } from 'svelte/store';

export type Role = 'user' | 'assistant';

export interface Usage {
  inputTokens: number;
  outputTokens: number;
  firstTokenMs: number;
  totalMs: number;
}

export type StepKind = 'tool_call' | 'tool_result' | 'permission';

export interface AgentStep {
  kind: StepKind;
  name: string;
  arguments?: string;
  result?: string;
  isError?: boolean;
  // Only on permission steps: the round-trip id used by RespondPermission,
  // and the user's resolved decision once they click. `permissionId` is
  // cleared once resolved so the UI knows to drop the buttons.
  permissionId?: string;
  permissionStatus?: 'pending' | 'approved' | 'denied';
}

export interface Message {
  role: Role;
  content: string;
  thinking?: string;
  usage?: Usage;
  steps?: AgentStep[];
  // Number of image attachments that rode with this user message at
  // send time. Stored as a count rather than the bytes themselves
  // because localStorage has a ~5MB origin quota that images blow
  // through quickly. Persistence-of-bytes is §7.4.
  attachmentCount?: number;
}

export interface Chat {
  id: string;
  title: string;
  messages: Message[];
  createdAt: number;
  updatedAt: number;
  tokensIn: number;
  tokensOut: number;
  // Active persona for this chat. When set, the persona's systemPrompt
  // replaces Settings.system in the next request. Mutually exclusive
  // with agentId. Undefined = "Default" mode.
  personaId?: string;
  // Active agent for this chat. When set, the agent's systemPrompt
  // replaces Settings.system AND the request routes through StreamAgent
  // with the agent's tool allowlist (instead of all available tools).
  // Mutually exclusive with personaId.
  agentId?: string;
}

// Persona is a tool-less role: a named, pre-packaged system prompt
// shaping the model's voice or output structure. Routes through the
// non-agent path (StreamChat). See docs/dev/personas_and_agents.md.
export interface Persona {
  id: string;
  name: string;
  description?: string;
  systemPrompt: string;
  builtin?: boolean;
}

// Agent is a persona that also carries a tool allowlist. Routes through
// the agent path (StreamAgent / ReAct loop) with the allowlist replacing
// "all available tools". The categorical distinction from Persona —
// presence-of-tools — is what enables future composition (8.3).
export interface Agent {
  id: string;
  name: string;
  description?: string;
  systemPrompt: string;
  // Allowlist of tool names. Must be non-empty (validated at create
  // time); an agent with no tools is a Persona by definition.
  tools: string[];
  builtin?: boolean;
  // Future (8.3): child agent ids for pipeline / host-and-specialists
  // composition. Reserved; not used yet.
  composedOf?: string[];
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
  // Tool names the user has previously clicked "Always approve" for.
  // Forwarded to App.StreamAgent on each run so the Go-side gate can
  // skip prompting for them. Persisted in localStorage.
  autoApproveTools: string[];
  // User's persona library: builtin catalog plus any custom personas
  // the user has created. Builtins are regenerated on Reset; custom
  // entries are wiped. See docs/dev/personas_and_agents.md.
  personas: Persona[];
  // User's agent library: same persistence semantics as personas.
  agents: Agent[];
}

// BUILTIN_PERSONAS is the ship-in catalog. Ids are stable across
// versions so Chat.personaId references survive upgrades. Builtins are
// merged into Settings.personas on every load — if the user has deleted
// a builtin (which is disallowed in the UI but possible via storage
// edits), it reappears next launch.
export const BUILTIN_PERSONAS: Persona[] = [
  {
    id: 'builtin-editor',
    name: 'Editor',
    description: 'Proofreads and clarifies prose without adding new content.',
    systemPrompt:
      'You are a careful editor. Improve the clarity, concision, and flow of the user\'s prose without changing its meaning, voice, or factual content. Do not add new ideas, examples, or arguments. When in doubt about an intentional stylistic choice, ask before changing it. Output the edited text directly, followed by a short bulleted list of the substantive changes you made.',
    builtin: true,
  },
  {
    id: 'builtin-code-reviewer',
    name: 'Code Reviewer',
    description: 'Reviews code for correctness and readability; does not write code.',
    systemPrompt:
      'You are a code reviewer. Read the user\'s code and identify, in order: correctness bugs, security issues, readability problems, and stylistic inconsistencies. Cite line numbers or symbols when pointing at specific issues. Do not rewrite the code — describe the change instead. If the code looks fine, say so plainly rather than inventing nitpicks.',
    builtin: true,
  },
  {
    id: 'builtin-researcher',
    name: 'Researcher',
    description: 'Explains concepts with citations, alternatives, and clarifying questions.',
    systemPrompt:
      'You are a careful researcher. Before answering substantive questions, ask one or two clarifying questions if the request is ambiguous. When you answer, cite sources or note when a claim is your inference rather than established fact. Always present at least one alternative framing or counter-argument. Acknowledge uncertainty rather than fabricating confidence.',
    builtin: true,
  },
  {
    id: 'builtin-concise',
    name: 'Concise',
    description: 'Three sentences or fewer unless asked to expand.',
    systemPrompt:
      'Answer in three sentences or fewer. Skip preambles, restating the question, and pleasantries. Use bullet points only if the user asks for a list. If a complete answer genuinely requires more space, say so in one sentence and ask whether to expand.',
    builtin: true,
  },
];

// BUILTIN_AGENTS is the ship-in agent catalog. Each agent's `tools`
// list references tool names registered in app.go::builtinTools; if a
// referenced tool is missing at runtime (e.g. quarto isn't installed)
// the agent will be greyed out in the picker — see findInstalledAgent
// resolution in App.svelte.
export const BUILTIN_AGENTS: Agent[] = [
  {
    id: 'builtin-quarto-author',
    name: 'Quarto Author',
    description: 'Drafts and renders Quarto documents (html, pdf, pptx, docx).',
    systemPrompt:
      'You are a Quarto author. The user describes a document; you produce a complete .qmd source (YAML frontmatter + markdown body) and call `quarto_render` with `content` set to that source so the document renders to disk. Choose the output format from the user\'s ask (default html when unspecified). Pin format-specific options in the YAML rather than passing them as separate arguments. After rendering succeeds, surface the markdown link the tool returns verbatim — relative paths or bolded filenames will not be clickable.',
    tools: ['quarto_render'],
    builtin: true,
  },
  {
    id: 'builtin-time-aware',
    name: 'Time-aware assistant',
    description: 'A general assistant that knows the current date and time.',
    systemPrompt:
      'You are a helpful assistant with access to the current wall-clock time via the `current_time` tool. Call it whenever the user asks about the current time / date or whenever recency matters for an answer (deadlines, "today", "this year"). Otherwise answer normally without invoking tools.',
    tools: ['current_time'],
    builtin: true,
  },
];

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

// Models known to accept image input. Used to gate the composer's
// paperclip / drop-zone affordances and warn before sending attachments
// that a text-only model would silently drop or error on. Conservative
// allowlist: opt-in per model id; unknown models are treated as
// text-only. Maintained alongside the provider model menus in app.go.
export const MULTIMODAL_MODELS = new Set<string>([
  // Anthropic Claude 4.x family — all support vision per Anthropic docs.
  'claude-haiku-4-5',
  'claude-sonnet-4-6',
  'claude-opus-4-7',
  // OpenAI GPT-5.x family — vision-capable.
  'gpt-5.4-nano',
  'gpt-5.4-mini',
  'gpt-5.4',
  'gpt-5.4-pro',
  'gpt-5.5',
  'gpt-5.5-pro',
]);

export function isMultimodal(model: string): boolean {
  return MULTIMODAL_MODELS.has(model);
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
  autoApproveTools: [],
  personas: BUILTIN_PERSONAS,
  agents: BUILTIN_AGENTS,
};

function loadSettings(): Settings {
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
      const customPersonas = userPersonas.filter(p => !builtinPersonaIds.has(p.id));
      merged.personas = [...BUILTIN_PERSONAS, ...customPersonas];
      const userAgents: Agent[] = Array.isArray(merged.agents) ? merged.agents : [];
      const builtinAgentIds = new Set(BUILTIN_AGENTS.map(a => a.id));
      const customAgents = userAgents.filter(a => !builtinAgentIds.has(a.id));
      merged.agents = [...BUILTIN_AGENTS, ...customAgents];
      return merged;
    }
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

// setChatPersona binds a persona to the active chat. Pass undefined to
// clear (Default mode). Setting a persona clears any agent (mutually
// exclusive). Bumps updatedAt so the chat sorts to the top.
export function setChatPersona(id: string, personaId: string | undefined) {
  chats.update(cs =>
    cs.map(c => (c.id === id ? { ...c, personaId, agentId: undefined, updatedAt: Date.now() } : c))
  );
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
// available; non-empty = agent should be greyed out / disabled in the
// picker with the missing names surfaced.
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
    };
    return { ...s, agents: [...s.agents, copy] };
  });
  return newId;
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
