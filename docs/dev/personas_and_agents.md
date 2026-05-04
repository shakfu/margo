# Personas and Agents

**Status: design doc, not yet implemented.** Tracks decisions made during
the §8 design pass so the eventual implementation lands cleanly. Once the
work ships, this doc converts to a behavior reference (mirroring the
post-implementation tone of `agents_and_tools.md`).

## The split

A **Persona** is a tool-less role: a named, pre-packaged system prompt
that shapes the model's voice, expertise, or output structure. Examples:
"Editor", "Code Reviewer", "Researcher", "Concise". Persona-mode
conversations route through the existing `StreamChat` path — no
multi-step loop, no tool callbacks, just a different system prompt.

An **Agent** is a persona that also carries a tool allowlist: a named
*capability* with a curated set of tools it can call. Examples: "Quarto
Author" (tools: `quarto_render`), "Time-aware assistant" (tools:
`current_time`). Agent-mode conversations route through `StreamAgent`
(the ReAct loop) with the agent's tool allowlist as `toolNames`.

The two are kept as **distinct types**, not one type with an optional
`tools` field, because:

1. The presence of tools is a categorical change in behavior (different
   Wails route, different UI affordances, different trust requirements
   via the permission middleware), not a quantitative one.
2. It keeps the future composition path open: a composed agent has
   sub-`Agent`s, never sub-`Persona`s — the unit of composition is the
   tool-bearing artifact.
3. The "persona with empty tool list" footgun is impossible to express,
   so it can't accidentally be created.

A persona is *not* a parent class of an agent. Their system prompts read
differently (personas are voice/perspective; agents are directive and
name their tools), and an "Agent extends Persona" model encourages
sharing a system prompt that fits neither role well.

## Data model

```ts
// frontend/src/lib/store.ts

export interface Persona {
  id: string;
  name: string;            // "Editor"
  description?: string;    // shown as picker subtitle
  systemPrompt: string;    // replaces (not appended to) Settings.system when active
  builtin?: boolean;       // builtins are read-only; user duplicates to customise
}

export interface Agent {
  id: string;
  name: string;            // "Quarto Author"
  description?: string;
  systemPrompt: string;
  tools: string[];         // allowlist of tool names; empty rejected at create-time
  builtin?: boolean;
  composedOf?: string[];   // future (8.3): child agent ids for host/specialist or pipeline
}
```

Two new fields on `Settings`:

```ts
personas: Persona[];      // user library (builtins + custom)
agents: Agent[];          // user library (builtins + custom)
```

Two new optional fields on `Chat`:

```ts
personaId?: string;       // active persona for this chat
agentId?: string;         // active agent for this chat — mutually exclusive with personaId
```

### Persistence

- `Settings.personas` and `Settings.agents` ride the existing
  `margo:settings:v1` localStorage entry. No schema bump for
  migration — both fields default to `[]` and are merged with
  `defaults` in `loadSettings()`.
- `Chat.personaId` / `Chat.agentId` ride `margo:chats:v1`. Both
  default to `undefined`. Existing chats migrate cleanly; their
  behavior is unchanged (no role active = current default mode).
- Reset (`resetApp`) clears both keys, regenerating builtins on next
  load.

### Validation

- `Persona.systemPrompt` may be empty (treats persona as "no-op
  persona", useful only as a placeholder; arguably not worth allowing
  — block at create-time).
- `Agent.tools` must be non-empty. An agent with no tools is a
  persona by definition.
- `Agent.tools` entries must resolve against `App.Tools()` at run
  time; missing entries surface as a warning in the agent picker
  ("requires `quarto_render` which is not installed") and the agent
  is greyed out.
- `id` is a UUID generated at create time. Builtins use stable
  hand-picked ids (`builtin-editor`, `builtin-quarto-author`) so
  references survive ship-version updates.

## Three modes

The role picker in the composer collapses the existing `agentMode`
checkbox into a single dropdown:

| Mode    | Active selection      | System prompt source              | Tools                      | Wails route   |
|---------|-----------------------|-----------------------------------|----------------------------|---------------|
| Default | none                  | `Settings.system` (free-form)     | none                       | `StreamChat`  |
| Persona | `Chat.personaId`      | `Persona.systemPrompt` (replaces) | none                       | `StreamChat`  |
| Agent   | `Chat.agentId`        | `Agent.systemPrompt` (replaces)   | `Agent.tools` allowlist    | `StreamAgent` |

Switching modes mid-chat is allowed; it changes the next request only.
The transcript records a "role changed to X" marker so the user
remembers which messages ran under which role.

### System-prompt resolution

When a persona or agent is active, **its `systemPrompt` fully replaces**
the global `Settings.system`. Rationale: persona/agent authors write
directive prompts ("You output structured JSON, never prose") that
conflict in unpredictable ways with arbitrary user additions to
`Settings.system`. Replacement is the predictable default.

A "fall back to default system prompt" checkbox per persona/agent is
deferred until anyone asks. If added, it should be opt-in per role so
the global default doesn't bleed into every role.

### Tool gating

Agent mode resolves the request's tool list as
`Agent.tools ∩ App.Tools()` — the intersection guards against
roles referencing tools that have been removed (e.g.
`quarto_render` when quarto isn't installed). Empty intersection
fails fast with a UI-visible error rather than silently degrading
to text-only.

The existing permission middleware (`agent.permissionMiddleware`) and
`autoApproveTools` list are unchanged: a tool's read-only status and
its trust state are properties of the *tool*, not the *agent* that
calls it. Trusting `quarto_render` once means it's trusted regardless
of which agent invokes it.

## UX

### Role picker (composer)

A new dropdown next to the model badge in the composer header,
labelled by the active role's name (or "Default" when nothing is
active). Click opens a Melt UI select with two grouped sections:

```
┌─ Role ─────────────────────┐
│  ⚪ Default                 │
│ ── Personas ──             │
│  📝 Editor                 │
│  🔍 Code Reviewer           │
│  🎓 Researcher              │
│ ── Agents ──                │
│  📊 Quarto Author     [1]   │  ← bracket = number of tools
│  🕐 Time-aware       [1]    │
│ ─────────────────           │
│  + New persona / agent…     │
└────────────────────────────┘
```

Active selection persists per-chat (`Chat.personaId` /
`Chat.agentId`). Default for a brand-new chat is "Default" — users
opt in.

### Settings → Agents tab (revised)

The existing **Agents** tab gains two new collapsible sections above
"Trusted tools":

- **Personas** — list of personas with name + description + a
  preview of the system prompt's first line. Each row has Edit /
  Duplicate / Delete (Delete disabled for builtins).
- **Agents** — list of agents with name + tool count + description.
  Same row actions. Edit opens a modal with name, description,
  system prompt (textarea), and a tool allowlist (multi-select
  populated from `App.Tools()`).

A **"+ New persona"** / **"+ New agent"** button at the bottom of
each section opens the same modal in create mode.

The "Trusted tools" section stays where it is — it's tool-trust
state, not role state, but lives in this tab because it shares
context with agent-mode usage.

### Default catalog (proposed)

Personas (small, opinionated):

- **Editor** — proofreads and clarifies prose; never adds new
  content.
- **Code Reviewer** — reviews code diffs, focuses on correctness
  and readability, doesn't write code.
- **Researcher** — explains concepts with citations and
  alternatives, asks clarifying questions before answering.
- **Concise** — answers in ≤3 sentences unless asked to expand.

Agents:

- **Quarto Author** — writes Quarto documents and renders them
  (tools: `quarto_render`).
- **Time-aware assistant** — knows the current date/time (tools:
  `current_time`).

Anti-pattern: shipping ten personas. The defaults exist so users
have something to copy-and-customise; they're not the curated
canon. Six is plenty.

## Implementation layers

### Frontend (most of the work)

1. Extend `Settings` interface, add defaults, persist.
2. Extend `Chat` interface, add the two optional fields.
3. New `lib/personas.ts` and `lib/agents.ts` — registries, default
   catalog, validation.
4. Role picker component in the composer header.
5. Settings panel sections (Personas, Agents) with create/edit/delete
   modals.
6. `send()` resolves the active role and constructs the request:
   - Default: existing path.
   - Persona: replace `system` arg to `StreamChat`.
   - Agent: replace `system` and pass agent's tool allowlist as
     `toolNames`; force `agentMode` semantics.

### Go side (small)

The existing `StreamChat` and `StreamAgent` already take a `system`
string and (for agent) a `toolNames []string`. **No new Wails methods
required for 8.1 or 8.2.** Persona/agent definitions live entirely
on the frontend; resolution happens before submitting to the bound
methods.

This is deliberate: keeping the Go side ignorant of personas/agents
means custom roles persist in localStorage without a Go-side schema,
and the Go layer stays simple. When **8.3 (composition)** lands,
some Go-side work appears: a multi-agent runner that knows how to
hand off between sub-agents. Until then, no.

### Existing `agentMode` checkbox

Removed. Selecting an agent in the role picker is the new way to
enable tools. The implicit contract:

- "Default" / persona modes route through `StreamChat`.
- Any agent route through `StreamAgent` regardless of whether the
  user previously had `agentMode` on.

Migration for existing chats: `Chat.personaId === undefined &&
Chat.agentId === undefined` preserves whatever the legacy
`agentMode` produced (full tool access via `StreamAgent` if the
chat's last setting had it on). Once a role is explicitly chosen,
the legacy flag is ignored. The `agentMode` field on `Settings` is
deprecated but kept for one ship cycle so cross-version downgrades
don't lose state.

## Sequenced rollout

### 8.1 Personas

Smallest user-visible slice. No Go-side changes. Ship the data
model, the default catalog, the role picker (with only the
"Personas" group populated), the settings UI for create/edit/delete,
and the system-prompt-replacement wiring in `send()`.

Coverage: snapshot test of `loadSettings()` defaults including
builtin personas; component test that selecting a persona
populates the request's `system` arg.

### 8.2 Agents

Same shape but with the tool allowlist. The role picker grows the
"Agents" group. Settings UI grows an "Agents" section with
tool-allowlist multi-select. `send()` routes agent-mode requests
through `StreamAgent` with the resolved tool list. Remove the
`agentMode` checkbox from the composer.

Coverage: validation test for `Agent.tools` non-empty and
intersection-with-`App.Tools()` resolution; integration test that
selecting an agent both sets `system` and constrains
`toolNames`.

### 8.3 Composition

Reopen this section once 8.1 and 8.2 have shipped and seen real
use. Two flavors to design for:

- **Pipeline** (sequential): `Agent A → output → Agent B → output
  → user`. Useful when the work decomposes into ordered stages
  (research → outline → draft).
- **Host / specialists** (hierarchical): `Host → routes to → A or
  B based on the request`. Maps directly to Eino's
  `flow/agent/multiagent/host` (existing TODO #6.7).

Both flavors fit the same data model with `composedOf: string[]`
populated. The runner switches based on a discriminator field
(`composition: "pipeline" | "host"`) that 8.3 introduces. Until
then, `composedOf` is reserved.

This is also where TODO #6.5 (custom graphs / plan-then-execute)
lands naturally: a planner agent is just an agent whose runner is
a `compose.Graph` instead of a ReAct loop. The user-facing model
stays "pick a role"; what's behind the role becomes pluggable.

## Tradeoffs and open questions

- **Role picker location**: composer header vs new-chat dialog.
  Composer header is the better default — switching mid-chat is a
  real workflow ("I want this drafted as a Researcher, then
  cleaned up by the Editor"). New-chat-only would force chat
  fragmentation.
- **System prompt: replace vs prepend?** Decided: replace. See
  rationale in "System-prompt resolution" above.
- **Tools: strict allowlist vs additive?** Decided: strict. The
  whole point of an agent is "this role + these tools, nothing
  else." Additive ("agent's tools + everything") collapses agents
  into "tagged free-form chat".
- **Custom personas: localStorage vs disk file?** Start with
  localStorage. Add JSON import/export when users ask (signal
  comes from "I want to share my custom Researcher with my team"
  reports, or "I lost everything when I clicked Reset").
- **Per-role model preference?** A persona/agent could pin a
  preferred model (e.g. Concise → fastest model, Researcher →
  largest context). Useful, but adds UX surface (does the user's
  manual model choice override the role's?). Defer until the
  default catalog gives clear signal.
- **Per-role sampling overrides?** Same shape as model preference
  — Editor might want temperature 0.2 by default. Defer for the
  same reason.

## Anti-patterns to avoid

- **Don't merge `Persona` and `Agent` into one type with optional
  `tools`.** The categorical distinction is the whole point of
  the design.
- **Don't let an agent extend a persona.** Their system prompts
  serve different purposes; sharing them encourages prompts that
  fit neither role well.
- **Don't ship custom personas/agents that overwrite each other on
  Reset.** Builtins regenerate (always identical to ship version);
  user-created entries get cleared. Persistence beyond Reset
  belongs in the eventual JSON-export feature, not in
  Reset's behavior.
- **Don't add a "system prompt fallback" checkbox by default.**
  Replacement is the simpler mental model; opt-in fallback can
  land if anyone asks.
- **Don't bake tool definitions into agents.** An agent references
  tools by name; the tool itself stays in `app.go::builtinTools`.
  Otherwise removing a tool means hunting through every agent
  definition.

## Related TODOs

- **§8.1 Personas, §8.2 Agents, §8.3 Composition** — the rollout
  sequence proposed in this doc. To be added to TODO.md alongside
  this commit.
- **TODO #6.5 (custom graphs)** — natural home for plan-then-execute
  as a composed agent type, once 8.3 lands.
- **TODO #6.7 (multi-agent host + specialists)** — collapses into
  8.3's "host / specialists" composition flavor; design once, ship
  once.
- **TODO #6.10 (tool middleware permission prompts)** — already
  shipped. Personas/agents inherit the permission flow unchanged.
