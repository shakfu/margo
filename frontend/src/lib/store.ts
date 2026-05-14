import { writable, derived } from 'svelte/store';

export type Role = 'user' | 'assistant';

export interface Usage {
  inputTokens: number;
  outputTokens: number;
  firstTokenMs: number;
  totalMs: number;
}

export type StepKind = 'tool_call' | 'tool_result' | 'tool_stream' | 'tool_retrieve' | 'permission';

export interface RetrievalHit {
  path: string;
  doc?: string;
  score: number;
  snippet?: string;
}

export interface AgentStep {
  kind: StepKind;
  name: string;
  arguments?: string;
  result?: string;
  isError?: boolean;
  // Live streaming buffer for a tool_call whose backing tool is a
  // StreamableTool. Accumulates incoming `tool_stream` chunks until the
  // matching `tool_result` arrives, at which point the final concatenated
  // text lives in `result` and `stream` is no longer rendered separately.
  stream?: string;
  // Structured retrieval matches, attached when a `tool_retrieve` event
  // arrives for this tool_call. When present, the step card renders the
  // hit list instead of the raw result text; the result text still lives
  // in `result` for the model's continuation.
  hits?: RetrievalHit[];
  // Only on permission steps: the round-trip id used by RespondPermission,
  // and the user's resolved decision once they click. `permissionId` is
  // cleared once resolved so the UI knows to drop the buttons.
  permissionId?: string;
  permissionStatus?: 'pending' | 'approved' | 'denied';
}

// StoredAttachment mirrors main.StoredAttachment from the Wails bindings:
// the on-disk record of an attachment that rode with a user message. The
// bytes themselves live under os.UserConfigDir()/Margo/attachments/<chatID>/
// keyed by `path`; localStorage holds only this lightweight record so the
// chat history survives a reload without blowing the ~5 MB origin quota.
export interface StoredAttachment {
  path: string;
  name: string;
  mimeType: string;
  size: number;
}

export interface Message {
  role: Role;
  content: string;
  thinking?: string;
  usage?: Usage;
  steps?: AgentStep[];
  // Attachments that rode with this user message. Bytes live on disk;
  // see StoredAttachment. Optional + tolerated as absent on
  // pre-§7.4 messages, which fall back to the legacy `attachmentCount`.
  attachments?: StoredAttachment[];
  // Legacy: count-only badge from before §7.4. New messages set
  // `attachments` instead and ignore this field on render.
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
  // Workspace scope (7.1.b). Undefined = global (visible in every
  // workspace). A workspace id = visible only in that workspace.
  // Builtins are always global; the UI refuses to scope them.
  workspaceId?: string;
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
  // Workspace scope (7.1.b). Same semantics as Persona.workspaceId.
  workspaceId?: string;
}

// Workspace is a named, optionally directory-bound container for chats.
// Each workspace's chats persist under a dedicated localStorage key so
// switching workspaces swaps the chat list. The `dir` field is reserved
// for later slices (per-workspace RAG); 7.1.b adds scoped personas/
// agents; 7.1.c adds `overrides` for per-workspace settings.
// See REVIEW.md §7.1.
export interface Workspace {
  id: string;
  name: string;
  dir?: string;
  createdAt: number;
  updatedAt: number;
  // Settings the active workspace overrides. Only keys listed in
  // OVERRIDABLE_KEYS are honoured by effectiveSettings; everything
  // else falls through to global Settings. (7.1.c)
  overrides?: WorkspaceOverrides;
  // Names of agent tools enabled in this workspace's chats (§9.3).
  // Undefined means "all available tools enabled" — the migration-safe
  // default for workspaces created before §9.3 shipped, and the
  // intended baseline for new workspaces unless the user opts to
  // narrow the palette. The runtime filter is
  //   resolvedTools = availableTools ∩ enabledTools (if set)
  // so a tool unregistered at startup (e.g. quarto_render without
  // the quarto CLI) drops out regardless of the toggle.
  enabledTools?: string[];
}

// WorkspaceOverrides is the subset of Settings a workspace may shadow.
// Kept narrow on purpose: theme, panel toggles, persona/agent libraries,
// and the workspaces table itself are user-scoped state, not workspace-
// scoped. Sampling pointer fields stay nullable to mirror Settings.
export interface WorkspaceOverrides {
  provider?: string;
  model?: string;
  system?: string;
  maxTokens?: number;
  temperature?: number | null;
  topP?: number | null;
  stopSequences?: string[];
  thinkEnabled?: boolean;
  thinkBudget?: number;
}

// OVERRIDABLE_KEYS is the runtime mirror of WorkspaceOverrides' keys,
// used by effectiveSettings to project overrides onto the global
// settings record. Kept manually in sync (TS doesn't expose interface
// keys at runtime).
export const OVERRIDABLE_KEYS = [
  'provider', 'model', 'system',
  'maxTokens', 'temperature', 'topP', 'stopSequences',
  'thinkEnabled', 'thinkBudget',
] as const satisfies ReadonlyArray<keyof WorkspaceOverrides>;

// WorkspaceTemplate is a starter pack: a name + description, optional
// scoped personas/agents to install into the new workspace, and
// optional overrides. Picking a template at workspace creation time is
// equivalent to "create a workspace, then install these extras." (7.1.f)
//
// Personas and agents in `personas` / `agents` are written workspace-
// scoped (their `workspaceId` is set to the new workspace's id during
// install). They get fresh ids on install so re-using the same
// template produces independent copies. `id` and `builtin` on the
// template entries are ignored — they're factories, not records.
export interface WorkspaceTemplate {
  id: string;          // stable template id; used by the picker and analytics
  name: string;        // human-readable template name (also default workspace name)
  description: string;
  personas?: Omit<Persona, 'id' | 'builtin' | 'workspaceId'>[];
  agents?: Omit<Agent, 'id' | 'builtin' | 'workspaceId'>[];
  overrides?: WorkspaceOverrides;
}

// Stable id for the seeded default workspace. Chats migrated from the
// legacy `margo:chats:v1` key land here; this id is a deletion-blocked
// fixed point so migration logic doesn't need to invent one.
export const DEFAULT_WORKSPACE_ID = 'default';

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
  // Workspaces (7.1.a). Always non-empty: the seeded "Default"
  // workspace is re-asserted on every load so users can't end up
  // with zero workspaces and no chats key to write to.
  workspaces: Workspace[];
  activeWorkspaceId: string;
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

// LEGACY_BUILTIN_AGENT_IDS is the closed set of ids from the pre-§9.4
// BUILTIN_AGENTS catalog. These records bundled persona + tool
// allowlist and were retired entirely in §9.4 — they are not
// migrated into BUILTIN_PERSONAS because, per docs/concepts.md, a
// persona shapes voice, not behavior; "Quarto Author" and "Time-
// aware assistant" were tool-directives wearing persona costumes.
// The chat migration uses this set to clear `chat.agentId` cleanly
// when it points at one of these (rather than leaving a dangling
// `personaId` reference). Any new pre-§9.4 builtin ids would go
// here.
const LEGACY_BUILTIN_AGENT_IDS = new Set<string>([
  'builtin-quarto-author',
  'builtin-time-aware',
]);

// BUILTIN_AGENTS is retained as an empty array after §9.4 retired the
// bundled-Agent concept. The two former built-ins (Quarto Author,
// Time-aware assistant) moved to BUILTIN_PERSONAS above; runners are
// now picked per-turn via the slash-command grammar (`/agent`,
// `/agent-plan`, …) and tool palettes are workspace-level enable
// toggles in the Tools tab. The `Agent` type and `Settings.agents`
// field stay declared so legacy localStorage payloads deserialise
// cleanly during the load-time migration; they're scheduled for
// removal in a follow-up slice.
export const BUILTIN_AGENTS: Agent[] = [];

// WORKSPACE_TEMPLATES is the ship-in catalog (7.1.f). Each entry is a
// recipe: pick one in the workspace manage dialog and the new
// workspace is pre-populated. Templates are deliberately conservative
// — they don't reach for tools that may not be installed (quarto), and
// avoid overriding the global model/provider since those depend on
// what the user has configured. Add per-template overrides only when
// they're load-bearing for the use case.
//
// "Empty" is omitted: the manage dialog renders "Empty workspace" as
// the no-template option directly.
export const WORKSPACE_TEMPLATES: WorkspaceTemplate[] = [
  {
    id: 'tmpl-writing',
    name: 'Writing & editing',
    description: 'Long-form prose work. Pre-tunes the system prompt for clarity and adds a draft-revision persona.',
    personas: [
      {
        name: 'Draft Reviser',
        description: 'Proposes specific line-level revisions with reasons.',
        systemPrompt:
          "You are a careful prose reviser. For each piece of text the user submits, return: (1) the revised text, (2) a numbered list of substantive changes you made and why. Preserve voice and intent. Flag (don't silently fix) any factual claim you can't verify.",
      },
    ],
    overrides: {
      system: 'Default to clear, concrete prose. Avoid filler phrases ("it is important to note", "in summary"). Mirror the user\'s register; don\'t formalise casual writing.',
    },
  },
  {
    id: 'tmpl-code-review',
    name: 'Code review',
    description: 'Pull-request reviews. Pairs the builtin Code Reviewer with a stricter system prompt and thinking enabled.',
    personas: [
      {
        name: 'PR Reviewer',
        description: 'Reviews diffs with a focus on correctness, then readability.',
        systemPrompt:
          "You are reviewing a code change. Walk through the diff in this order: (1) correctness bugs, (2) security, (3) regressions in adjacent code, (4) readability. Cite line numbers. Don't rewrite the code — describe each change. If the diff is fine, say so plainly.",
      },
    ],
    overrides: {
      thinkEnabled: true,
      thinkBudget: 4096,
    },
  },
  {
    id: 'tmpl-research',
    name: 'Research',
    description: 'Open-ended investigation. Tunes for skeptical answers with citations and clarifying questions.',
    personas: [
      {
        name: 'Skeptical Researcher',
        description: 'Cites sources, marks inferences, and presents alternatives.',
        systemPrompt:
          "You are a careful researcher. Before answering substantive questions, ask one clarifying question if the request is ambiguous. Cite sources or note when a claim is your inference. Always present at least one alternative framing. Prefer 'I don't know' over speculation.",
      },
    ],
    overrides: {
      temperature: 0.3,
    },
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

const LEGACY_CHATS_KEY = 'margo:chats:v1';
const SETTINGS_KEY = 'margo:settings:v1';

// chatsKey returns the localStorage key holding chats for `workspaceId`.
// Format chosen so the legacy single-workspace key (`margo:chats:v1`)
// can be unambiguously distinguished from per-workspace keys.
export function chatsKey(workspaceId: string): string {
  return `margo:chats:${workspaceId}:v1`;
}

function uuid(): string {
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
function migrateLegacyChats(): void {
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
function migrateAgentIdsToPersonaIds(chats: Chat[]): Chat[] {
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

function loadChatsForWorkspace(workspaceId: string): Chat[] {
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
  workspaces: [{
    id: DEFAULT_WORKSPACE_ID,
    name: 'Default',
    createdAt: 0,
    updatedAt: 0,
  }],
  activeWorkspaceId: DEFAULT_WORKSPACE_ID,
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
      return merged;
    }
  } catch (_) {}
  return { ...defaults };
}

// Settings is loaded first so the chats store can scope to the active
// workspace. Legacy single-workspace chats migrate into the Default
// workspace's key on first run.
migrateLegacyChats();
const initialSettings = loadSettings();

export const settings = writable<Settings>(initialSettings);

// Module-scoped mirror of the active workspace id so the chats
// subscription writes to the correct key without `get(settings)` calls.
// Updated by the settings subscription below.
let currentActiveWorkspaceId = initialSettings.activeWorkspaceId;

// Suppression flag for chats writes during a workspace swap: when
// setActiveWorkspace replaces the chats store contents wholesale, the
// subscription would otherwise overwrite the *new* workspace's stored
// chats with the freshly-loaded list (a no-op write but conceptually
// wrong) — and worse, if the swap raced with a pending update, could
// stomp the wrong key. Set true around the swap; chats subscription
// returns early.
let suppressChatsWrite = false;

export const chats = writable<Chat[]>(loadChatsForWorkspace(currentActiveWorkspaceId));
chats.subscribe(cs => {
  if (suppressChatsWrite) return;
  try { localStorage.setItem(chatsKey(currentActiveWorkspaceId), JSON.stringify(cs)); } catch (_) {}
});

settings.subscribe(s => {
  currentActiveWorkspaceId = s.activeWorkspaceId;
  try { localStorage.setItem(SETTINGS_KEY, JSON.stringify(s)); } catch (_) {}
});

export const activeChatId = writable<string>('');

export const activeChat = derived(
  [chats, activeChatId],
  ([$chats, $id]) => $chats.find(c => c.id === $id) ?? null
);

export const activeWorkspace = derived(
  settings,
  $s => $s.workspaces.find(w => w.id === $s.activeWorkspaceId) ?? $s.workspaces[0],
);

// sessionOverrides is the in-memory override layer used while the
// Default workspace is active. Edits made to settings inputs in the
// right sidebar (Provider/Model/System/Sampling/Thinking) write here
// instead of mutating global Settings. Cleared on app reload — that
// "transient" property is the user-visible signal that you're in
// experiment mode. Non-Default workspaces ignore this layer entirely
// and use Workspace.overrides on disk. (Workspace UX flip.)
export const sessionOverrides = writable<WorkspaceOverrides>({});

// effectiveSettings projects per-scope overrides onto the global
// Settings record:
//   - Default workspace active  → global + sessionOverrides (transient)
//   - Other workspace active    → global + workspace.overrides   (sticky)
// Components that should respect overrides (App.svelte's send(),
// topbar badges, the workspace-mode SettingsPanel) read from this
// store. The Cmd+, dialog (mode='global') keeps reading raw `settings`
// to edit the true global defaults.
export const effectiveSettings = derived(
  [settings, activeWorkspace, sessionOverrides],
  ([$s, $ws, $sess]) => {
    const o: WorkspaceOverrides | undefined =
      $ws?.id === DEFAULT_WORKSPACE_ID ? $sess : $ws?.overrides;
    if (!o || Object.keys(o).length === 0) return $s;
    const out: Settings = { ...$s };
    for (const k of OVERRIDABLE_KEYS) {
      // hasOwnProperty is the right test: an override that is
      // explicitly null/0/'' is still an override (clears the global).
      // Missing key = no override.
      if (Object.prototype.hasOwnProperty.call(o, k)) {
        // TS can't narrow k against Settings union here without a
        // per-key switch; the cast is safe because OVERRIDABLE_KEYS
        // is statically typed as keyof WorkspaceOverrides ⊂ keyof Settings.
        (out as any)[k] = (o as any)[k];
      }
    }
    return out;
  },
);

// setEffectiveOverride routes a write to the right scope: Default
// workspace → sessionOverrides (transient); other workspace →
// Workspace.overrides on disk. Pass undefined to clear (parity with
// setWorkspaceOverride). Used by the workspace-mode SettingsPanel.
export function setEffectiveOverride<K extends keyof WorkspaceOverrides>(
  key: K,
  value: WorkspaceOverrides[K] | undefined,
) {
  const wsId = currentActiveWorkspaceId;
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

// visiblePersonas filters the persona library down to those that should
// appear in the picker for the given active workspace: anything global
// (workspaceId undefined) plus anything scoped to the active workspace.
// Builtins are global by construction. (7.1.b)
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
      workspaceId: src.workspaceId,
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
  if (id === currentActiveWorkspaceId) return;
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
  suppressChatsWrite = true;
  try { chats.set(next); } finally { suppressChatsWrite = false; }
  activeChatId.set(next[0]?.id ?? "");
}
