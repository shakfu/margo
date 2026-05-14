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

## Agent

An **agent** is a configuration that lets the model reason through
problems, create plans, and autonomously use tools to execute
multi-step tasks. An agent always runs *as* some persona — most often
the default 'assistant' persona, since the agent's value lies in the
tools and the control loop rather than in voice — but it may pair
with any persona when the task calls for a distinct tone.

Persona and agent describe orthogonal axes: a persona shapes *what
the model sounds like*; an agent shapes *what the model can do*. A
chat picks at most one of each. The two compose: a "Researcher"
persona paired with a web-search agent yields a researcher who can
browse; the same agent paired with the default persona is a
plain-spoken browser. Tools and the control loop come from the agent;
voice and parameter overrides come from the persona.

## Tools

**Tools** are external functions, APIs, or software systems that an
agent can use to extend its capabilities beyond generating text.

Tools exist independently of any agent. They are registered once, at
application startup, based on which capabilities are available on the
host system. An agent's configuration *references* tools by name —
its **allowlist** is the set of tools it is permitted to call during
a run. The same tool can appear in multiple agents' allowlists;
removing a tool from one agent has no effect on the others.

## Types of agents

Margo distinguishes agent *types* by the control loop that drives them.
A given agent record (in the UI: a saved configuration) picks one of
the types below as its runner; the rest of the agent's state — system
prompt, tool allowlist — is shared across types.

### ReAct agent

The default. Implements the **Reason + Act** loop: the model emits a
thought (optional preamble text), picks a tool, observes the result,
and decides whether to continue, call another tool, or produce a final
answer. The cycle repeats until the model emits a turn with no tool
calls.

Best for short tasks where the right next step is obvious from the
prior step — answering a question that needs one fact lookup, rendering
a single document, fetching one URL. ReAct's strength is its
simplicity; its weakness is that long chains drift, because each step
sees only its own immediate context, not a plan.

Implementation: `pkg/margo/agent/stream.go::StreamReact`, built on
Eino's `react.NewAgent`.

### Plan-then-execute agent

*Planned (TODO §6.5), not yet shipped.* A planner node generates a
structured task list up front; a worker node executes each step using
tools; a reducer node summarises the results.

Best for multi-step tasks where the steps are knowable in advance —
"summarise this PDF and extract the citations", "refactor every test
file in this folder". The plan provides global context the per-step
worker can refer back to, so the loop is less prone to drift than
ReAct on long jobs. The cost is a wasted planner call on tasks ReAct
would have handled in one turn, which is why type-per-agent selection
matters.

### Multi-agent (host + specialists)

*Planned (TODO §6.7 / §8.3), not yet shipped.* A **host** agent routes
each request to one of several specialist agents — e.g. a coder, a
researcher, a summariser — each of which is itself a ReAct agent with
its own tool allowlist.

Best when the tool count grows past ~10 and a single ReAct loop
starts mis-selecting tools, or when distinct sub-tasks want distinct
voices (a code-reviewer specialist plus a documentation specialist
under one host). The cost is a routing decision per turn and an extra
level of UI nesting (specialist activity rendered inside the host's
turn).

### Pipeline agent

*Planned (TODO §8.3), not yet shipped.* A fixed sequence of agents
where each agent's output feeds the next agent's input: `A → B → C →
user`. No routing; the order is hard-coded in the agent's
configuration.

Best for transformation chains where every input needs the same
sequence — "draft → fact-check → tighten" on every reply. Cheaper than
host-style routing (no decision call) but inflexible: every input
walks the whole pipeline whether it needs to or not.

## Chat

A **chat** is a single conversation thread: an ordered sequence of
turns between the user and the model, with a persistent context
window that carries from one turn to the next until the chat ends or
is reset.

A chat is where the other concepts meet. Each chat belongs to exactly
one workspace, runs under at most one persona and at most one agent
(both optional, both mutually exclusive of nothing else), and inherits
its workspace's knowledge sources. The chat is the smallest unit of
addressable conversational state.

## Workspaces

A **workspace** is a named context for chats. Each workspace has its
own chat history, its own knowledge index (see below), and optionally
its own settings overrides (model, temperature, system prompt). The
active workspace narrows the chat list and routes the active
knowledge-source list; switching workspaces effectively swaps the
sidebar and the agent's retrieval scope.

Personas and agents may be **global** (visible in every workspace) or
**workspace-scoped** (visible only when that workspace is active).
Built-in personas and agents are always global.

## Knowledge sources

A **knowledge source** is a file or directory the user has pointed
margo at for retrieval-augmented generation. Indexed content is
chunked, embedded via OpenAI's embedding model, and stored in a
per-workspace vector collection on disk. Agents that include the
`search_knowledge` tool in their allowlist can query the active
workspace's collection at run time.

Knowledge sources are workspace-scoped — there is no global
collection. An agent's retrieval surface depends on which workspace is
active when the chat runs.

## How the pieces compose

A chat selects, independently, **a persona** and **an agent**. Either
may be omitted; both may be present.

- **Neither**: the chat runs as the default 'assistant' persona, with
  no tools and no agent loop. Plain conversation.
- **Persona only**: voice changes, but no tools are equipped and no
  agent loop runs. A "Code Reviewer" persona, for example, asks for
  diffs and critiques them in character — but cannot fetch them.
- **Agent only**: the chat runs the agent's allowlisted tools through
  its control loop, with the default persona supplying voice.
- **Both**: the agent's tools and control loop combine with the
  persona's voice. The "Researcher persona + web-search agent"
  example above is this case.

The workspace contributes the knowledge sources the
`search_knowledge` tool reaches (when the active agent includes it
in its allowlist) and the default settings the chat inherits.

The result: persona, agent, tools, and knowledge sources are
independent axes a chat layers together. None of them owns the
others, and any can be changed without touching the rest.
