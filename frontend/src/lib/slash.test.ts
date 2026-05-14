// Hand-runnable assertions for slash.ts (TODO §9.2).
//
// The frontend has no test runner configured (see TODO §9.2 follow-up).
// This file is structured as bare top-level assertions that throw on
// failure so it can be executed with `tsx slash.test.ts` from the
// frontend/ directory once a runner lands, and read as
// documentation in the meantime. Each block stays small enough that a
// failing assertion's message identifies which case broke.
//
// To exercise manually:
//   cd frontend && npx tsx src/lib/slash.test.ts

import { parseSlash, slugify, SLASH_COMMANDS } from './slash';

function assert(cond: unknown, msg: string): void {
  if (!cond) {
    throw new Error('assertion failed: ' + msg);
  }
}

function assertEq<T>(got: T, want: T, msg: string): void {
  if (JSON.stringify(got) !== JSON.stringify(want)) {
    throw new Error(`${msg}: got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`);
  }
}

// --- non-slash input falls through ---

assertEq(parseSlash(''), null, 'empty');
assertEq(parseSlash('hello'), null, 'plain word');
assertEq(parseSlash('  hello /agent'), null, 'slash not at start');
assertEq(parseSlash('/etc/passwd is sensitive'), null, 'literal path containing slashes');

// --- /agent forms ---

assertEq(
  parseSlash('/agent'),
  { kind: 'agent', runnerType: 'react', task: '' },
  '/agent bare'
);
assertEq(
  parseSlash('/agent draft the doc'),
  { kind: 'agent', runnerType: 'react', task: 'draft the doc' },
  '/agent with task'
);
assertEq(
  parseSlash('/agent-plan summarise this'),
  { kind: 'agent', runnerType: 'plan', task: 'summarise this' },
  '/agent-plan typed'
);
assertEq(
  parseSlash('/agent-workflow do it'),
  { kind: 'agent', runnerType: 'workflow', task: 'do it' },
  '/agent-workflow typed'
);

// Case insensitivity on the command word; argument case is preserved.
assertEq(
  parseSlash('/AGENT Hello World'),
  { kind: 'agent', runnerType: 'react', task: 'Hello World' },
  'uppercase /AGENT'
);

// Whitespace tolerance.
assertEq(
  parseSlash('  /agent   draft  '),
  { kind: 'agent', runnerType: 'react', task: 'draft' },
  'leading/trailing whitespace'
);

// Multi-line task: shift-enter content rides through.
assertEq(
  parseSlash('/agent line1\nline2'),
  { kind: 'agent', runnerType: 'react', task: 'line1\nline2' },
  'multi-line task'
);

// --- /persona forms ---

assertEq(
  parseSlash('/persona'),
  { kind: 'persona', slug: '' },
  '/persona bare (clear)'
);
assertEq(
  parseSlash('/persona researcher'),
  { kind: 'persona', slug: 'researcher' },
  '/persona with slug'
);

// --- /default and /clear ---

assertEq(parseSlash('/default'), { kind: 'clear' }, '/default');
assertEq(parseSlash('/clear'), { kind: 'clear' }, '/clear');

// --- unknown commands ---

assertEq(
  parseSlash('/agnet draft'),
  { kind: 'unknown', word: 'agnet' },
  'misspelt /agnet'
);
assertEq(
  parseSlash('/foo'),
  { kind: 'unknown', word: 'foo' },
  'unknown /foo'
);

// --- slugify ---

assertEq(slugify('Code Reviewer'), 'code-reviewer', 'slugify basic');
assertEq(slugify('  Editor  '), 'editor', 'slugify trim');
assertEq(slugify("Bob's Persona!"), 'bob-s-persona', 'slugify punctuation');
assertEq(slugify(''), '', 'slugify empty');

// --- catalog sanity ---

assert(SLASH_COMMANDS.length >= 4, 'SLASH_COMMANDS catalog populated');
assert(
  SLASH_COMMANDS.every(c => c.command.startsWith('/') && c.description.length > 0),
  'catalog entries shaped correctly',
);

console.log(`slash.test: ${assert.toString().length > 0 ? 'all assertions passed' : ''}`);
