// Shared types for the chat/settings store.
//
// Split out of the former monolithic lib/store.ts. Import from
// `lib/store` (the index), not from these modules directly — the index
// is the module boundary consumers depend on.

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
  // Provider and model that produced this turn, recorded on assistant
  // messages at send time.
  //
  // Cost used to be computed by pricing the chat's running token totals
  // against whatever model was selected at render time, so switching
  // model mid-chat silently repriced the entire history. Pricing each
  // turn against the model that actually produced it is the only way
  // the number can be right.
  //
  // Absent on turns recorded before this shipped; costBreakdown buckets
  // those separately rather than inventing a rate for them.
  model?: string;
  provider?: string;
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
  // Persona id that newly-created chats in this workspace start
  // with. Set in the Settings → Roles → Personas sidebar; per-chat
  // override happens via `/persona <slug>`. Undefined means new
  // chats start with no persona (the default 'assistant' voice
  // from concepts.md). Dangling references (the persona was
  // deleted) are cleared on load.
  defaultPersonaId?: string;
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
  //
  // Tools that report allowsAlways=false never enter this list, and
  // loadSettings strips any that a previous version put there. The Go
  // gate enforces the same rule, so a stale entry is inert either way.
  autoApproveTools: string[];
  // Last model the user picked, per provider. `model` above is the
  // active one; this is what a provider switch restores.
  //
  // Without it, switching provider cleared the model and a reactive
  // snapped it to the catalog's first entry, so going away and coming
  // back lost the choice. That is a nuisance at 6 models and unusable
  // at OpenRouter's ~400.
  lastModelByProvider: Record<string, string>;
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
