// Slash-command parser (TODO §9.2).
//
// Recognised grammar (see docs/concepts.md, TODO §9):
//
//   /agent <task>             one-shot ReAct run with the task
//   /agent-<type> <task>      one-shot run through the named runner type
//   /persona <slug>           persistent: bind persona to the active chat
//   /persona                  persistent: clear the chat's persona
//   /default | /clear         clear persona + agent state for the chat
//
// Disambiguation rule: only the first whitespace-delimited token matters,
// AND it must match `^/[a-zA-Z][a-zA-Z0-9_-]*$`. That excludes literal-
// slash text like `/etc/passwd` (which contains slashes mid-token) from
// being treated as a command, while still catching typos like `/agnet`.
// Unknown command-shaped inputs return `{ kind: 'unknown' }` so the
// caller can surface an inline error rather than silently shipping the
// typo to the model.

export type SlashCommand =
  | { kind: 'agent'; runnerType: string; task: string }
  | { kind: 'persona'; slug: string } // empty slug means "clear persona"
  | { kind: 'clear' }                  // /default or /clear — clear persona + agent
  | { kind: 'unknown'; word: string }; // looks like a slash command but isn't recognised

const tokenRe = /^\/([a-zA-Z][a-zA-Z0-9_-]*)(?:\s+([\s\S]*))?$/;
const agentTypeRe = /^agent-([a-zA-Z][a-zA-Z0-9_-]*)$/;

/**
 * parseSlash decides whether `input` is a slash command and, if so,
 * what it means. Returns null for plain-text input (including literal
 * slash sequences like `/etc/passwd is sensitive`) — callers should
 * proceed with their normal send path in that case.
 */
export function parseSlash(input: string): SlashCommand | null {
  const trimmed = input.trim();
  if (!trimmed.startsWith('/')) return null;
  const m = trimmed.match(tokenRe);
  if (!m) {
    // Starts with `/` but the first token has slashes or other
    // non-command chars mid-token — treat as plain text. This is the
    // path `/etc/passwd is sensitive` takes.
    return null;
  }
  const word = m[1].toLowerCase();
  const rest = (m[2] ?? '').trim();

  if (word === 'agent') {
    return { kind: 'agent', runnerType: 'react', task: rest };
  }
  const agentMatch = word.match(agentTypeRe);
  if (agentMatch) {
    return { kind: 'agent', runnerType: agentMatch[1], task: rest };
  }
  if (word === 'persona') {
    return { kind: 'persona', slug: rest };
  }
  if (word === 'default' || word === 'clear') {
    return { kind: 'clear' };
  }
  return { kind: 'unknown', word };
}

/**
 * slugify turns a human-facing persona name ("Code Reviewer") into the
 * slash-friendly form ("code-reviewer") the user actually types.
 */
export function slugify(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');
}

/**
 * SLASH_COMMANDS is the catalog the autocomplete hint reads. Kept here
 * (not in a Svelte component) so the parser and the UI stay in lock-
 * step — adding a command means updating one list.
 */
export const SLASH_COMMANDS: Array<{ command: string; description: string }> = [
  { command: '/agent', description: 'Run the next message through ReAct.' },
  { command: '/agent-plan', description: 'Run through plan-then-execute (planner → executor → replanner).' },
  { command: '/agent-workflow', description: 'Run through the drafter → critic → refiner pipeline.' },
  { command: '/persona', description: 'Set or clear this chat\'s persona.' },
  { command: '/default', description: 'Clear persona for this chat.' },
];
