# Concepts

This document defines the primary concepts margo exposes — persona, agent,
tool, workspace, knowledge source — and the relationships between them.
It is the entry point for understanding what each surface in the UI
actually represents.

For implementation detail and design rationale, see
`docs/dev/personas_and_agents.md` and `docs/dev/agents_and_tools.md`.

## Persona

A **persona** is a simulated digital character or agent programmed to
interact with human-like personality, tone, and emotional expression.

In the context of this application, a persona is specified by an
unstructured or structured system prompt, and possibly by changes to the
default parameters.

The application defaults to the **'assistant' persona**, when the system
prompt is empty and the parameters are at a default setting.

Personas are activated at two scopes that compose:

- **Workspace default**: each workspace can pick one persona to seed
  *new chats* with. Set in the right-sidebar Roles tab. Existing
  chats are unaffected by changing this — it's a seed for the next
  chat created, not a live override. The sidebar lists a **Default**
  row (the built-in 'assistant' voice — no persona system prompt)
  alongside the named personas; selecting it as the workspace default
  means new chats start in plain-assistant mode.
- **Per-chat override**: inside any chat, typing
  `/persona <slug>` binds that persona to the conversation,
  superseding the workspace default for the rest of that chat.
  Reverting to the default voice in an existing chat is
  `/persona` (no argument) or `/default` — both clear the chat's
  persona binding so the bubbles read "ASSISTANT" again.

The active persona's name surfaces in place of "ASSISTANT" above
each assistant bubble, so the current voice is always visible.

## Agent

An **agent** is a control loop that lets the model reason through
problems, create plans, and autonomously use tools to execute
multi-step tasks. An agent always runs *as* some persona — most often
the default 'assistant' persona, since the agent's value lies in the
tools and the control loop rather than in voice — but it may pair
with any persona when the task calls for a distinct tone.

Persona and agent describe orthogonal axes: a persona shapes *what
the model sounds like*; an agent shapes *what the model can do*. The
two compose: a "Researcher" persona paired with a tool-using agent
yields a researcher who can browse; the same agent paired with the
default persona is a plain-spoken browser.

Agents are **invoked per-turn** via slash commands, not configured
as long-lived records. Typing `/agent <task>` runs the next message
through the default ReAct control loop; `/agent-plan <task>` and
`/agent-workflow <task>` invoke the plan-execute and sequential-
workflow variants respectively. After the turn ends, the chat
returns to plain conversation under the active persona — there is no
"agent mode" to toggle off.

## Tools

**Tools** are external functions, APIs, or software systems that an
agent can use to extend its capabilities beyond generating text.

Tools exist independently of any agent. They are registered once, at
application startup, based on which capabilities are available on the
host system (e.g. the `quarto_render` tool is only registered when
the `quarto` CLI is on PATH). Each workspace then chooses, in the
sidebar Tools tab, which registered tools its agent runs may call —
the **enabled set**. A `/agent` invocation in that workspace sees
the enabled set as its full palette. The same tool can be enabled
in multiple workspaces simultaneously; the tool itself is shared,
only the per-workspace enablement is independent.

## Types of agents

Margo distinguishes agent *types* by the control loop that drives
them. Each type is selected per-turn via its own slash command. The
type determines the loop; the workspace's enabled tools and the
chat's active persona contribute behavior and voice on top.

### ReAct agent — `/agent <task>`

The default. Implements the **Reason + Act** loop: the model emits
a thought (optional preamble text), picks a tool, observes the
result, and decides whether to continue, call another tool, or
produce a final answer. The cycle repeats until the model emits a
turn with no tool calls.

Best for short tasks where the right next step is obvious from the
prior step — answering a question that needs one fact lookup,
rendering a single document, fetching one URL. ReAct's strength is
its simplicity; its weakness is that long chains drift, because
each step sees only its own immediate context, not a plan.

Implementation: `pkg/margo/agent/adk_runner.go::ReactRunner`,
built on Eino ADK's `ChatModelAgent`.

### Plan-then-execute agent — `/agent-plan <task>`

A planner sub-agent emits a structured task list; an executor
sub-agent runs each step against the workspace's enabled tools; a
replanner sub-agent decides after each pass whether to continue or
finalise a response. The three sub-agents share the same provider
client; the loop is capped at a configurable max iteration count.

Best for multi-step tasks where the steps are knowable in advance —
"summarise this PDF and extract the citations", "refactor every test
file in this folder". The plan provides global context the per-step
executor can refer back to, so the loop is less prone to drift than
ReAct on long jobs. The cost is a wasted planner call on tasks ReAct
would have handled in one turn, which is why selecting the runner
per-turn matters.

Implementation: `pkg/margo/agent/plan_runner.go::PlanExecuteRunner`,
wrapping Eino ADK's `prebuilt/planexecute.New`.

### Sequential workflow agent — `/agent-workflow <task>`

A fixed three-stage pipeline: **drafter → critic → refiner**. The
drafter produces an initial response (and may use tools); the critic
reviews it without tools; the refiner produces the final polished
version. Each stage is a separate sub-agent with its own system
prompt; the kit handles passing each stage's output forward as part
of the running conversation.

Best for transformation chains where the same structure helps every
input — drafting written work, generating proposals, polishing
explanations. Cheaper than the plan-execute loop (no planner call)
but inflexible: every input walks the whole pipeline whether it
needs to or not. The sub-agent chain is currently fixed; a future
slice will make it user-configurable.

Implementation: `pkg/margo/agent/workflow_runner.go::WorkflowRunner`,
wrapping Eino ADK's `NewSequentialAgent`.

### Multi-agent (host + specialists)

*Planned (TODO §8.3), not yet shipped.* A **host** agent routes
each request to one of several specialist agents — e.g. a coder, a
researcher, a summariser — each of which is itself a ReAct agent
with its own enabled-tool palette.

Best when the tool count grows past ~10 and a single ReAct loop
starts mis-selecting tools, or when distinct sub-tasks want
distinct voices (a code-reviewer specialist plus a documentation
specialist under one host). The cost is a routing decision per turn
and an extra level of UI nesting (specialist activity rendered
inside the host's turn).

## Chat

A **chat** is a single conversation thread: an ordered sequence of
turns between the user and the model, with a persistent context
window that carries from one turn to the next until the chat ends or
is reset.

A chat is where the other concepts meet. Each chat belongs to
exactly one workspace, runs under at most one persona at a time
(inherited from the workspace default, optionally overridden via
`/persona <slug>`), and may invoke an agent on any given turn via
`/agent`-family slash commands. The chat is the smallest unit of
addressable conversational state.

## Workspaces

A **workspace** is a named context for chats. Each workspace has its
own chat history, its own knowledge index (see below), its own
default persona, its own set of enabled tools, and optionally its
own settings overrides (model, temperature, system prompt). The
active workspace narrows the chat list, routes the active
knowledge-source list, and supplies the tool palette agents see —
switching workspaces effectively swaps the entire conversational
context.

Personas may be **global** (visible in every workspace) or
**workspace-scoped** (visible only when that workspace is active).
Built-in personas are always global.

## Knowledge sources

A **knowledge source** is a file or directory the user has pointed
margo at for retrieval-augmented generation. Indexed content is
chunked, embedded via OpenAI's embedding model, and stored in a
per-workspace vector collection on disk. When the `search_knowledge`
tool is enabled in the workspace's tool palette, any agent run in
that workspace can query the collection at run time.

Knowledge sources are workspace-scoped — there is no global
collection. An agent's retrieval surface depends on which workspace
is active when the chat runs.

## How the pieces compose

Four independent axes layer together on each turn:

- **Persona** sets the voice. Inherited from the workspace default
  when a chat is created; overridden per-chat by `/persona <slug>`.
- **Agent** sets the control loop. Selected per-turn by slash
  command (`/agent`, `/agent-plan`, `/agent-workflow`); absent on
  any turn that doesn't open with one of those commands, in which
  case the turn is plain chat.
- **Tools** are workspace-enabled. An agent invocation sees the
  workspace's enabled palette as its full set of callable tools.
- **Knowledge sources** are workspace-scoped. The
  `search_knowledge` tool, when enabled, queries the active
  workspace's collection.

Each turn type:

- **Plain turn** (no slash): the chat runs as its active persona,
  no tools equipped, no agent loop. A "Code Reviewer" persona, for
  example, asks for diffs and critiques them in character — but
  cannot fetch them.
- **`/agent <task>`**: the task runs through the ReAct loop with
  the workspace's enabled tools, the active persona supplying
  voice. After the turn ends, the chat returns to plain mode.
- **`/agent-plan <task>` / `/agent-workflow <task>`**: same shape
  as `/agent`, different control loop.

None of the axes owns the others. The persona can change without
touching the tool palette; the tool palette can change without
touching the persona; the agent runner is per-turn so it never
needs "changing" at all.
