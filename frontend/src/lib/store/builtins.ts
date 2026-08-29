// Ship-in catalogs: builtin personas, the drained agent list, and the
// workspace templates.
//
// Split out of the former monolithic lib/store.ts. Import from
// `lib/store` (the index), not from these modules directly — the index
// is the module boundary consumers depend on.

import type { Persona, Agent, WorkspaceTemplate } from './types';

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
export const LEGACY_BUILTIN_AGENT_IDS = new Set<string>([
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
