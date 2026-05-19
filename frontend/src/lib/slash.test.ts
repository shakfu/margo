// Vitest unit tests for slash.ts. Replaces the prior hand-runnable
// assertion file with the standard test-runner shape so failures land
// in `npm run test` output instead of being discovered manually.
//
// Coverage:
//   - parseSlash dispatches to the right command kind, tolerating
//     case, whitespace, and multi-line task bodies.
//   - Non-slash input (and slashes not at the start) falls through.
//   - slugify normalises names to the form persona ids use.
//   - SLASH_COMMANDS catalogue is non-empty and well-shaped.

import { describe, test, expect } from 'vitest';
import { parseSlash, slugify, SLASH_COMMANDS } from './slash';

describe('parseSlash', () => {
  test('non-slash input falls through', () => {
    expect(parseSlash('')).toBeNull();
    expect(parseSlash('hello')).toBeNull();
    expect(parseSlash('  hello /agent')).toBeNull();
    expect(parseSlash('/etc/passwd is sensitive')).toBeNull();
  });

  test('/agent dispatches to react runner', () => {
    expect(parseSlash('/agent')).toEqual({ kind: 'agent', runnerType: 'react', task: '' });
    expect(parseSlash('/agent draft the doc')).toEqual({
      kind: 'agent', runnerType: 'react', task: 'draft the doc',
    });
  });

  test('/agent-plan and /agent-workflow select their runners', () => {
    expect(parseSlash('/agent-plan summarise this')).toEqual({
      kind: 'agent', runnerType: 'plan', task: 'summarise this',
    });
    expect(parseSlash('/agent-workflow do it')).toEqual({
      kind: 'agent', runnerType: 'workflow', task: 'do it',
    });
  });

  test('command word is case-insensitive but task case is preserved', () => {
    expect(parseSlash('/AGENT Hello World')).toEqual({
      kind: 'agent', runnerType: 'react', task: 'Hello World',
    });
  });

  test('tolerates leading/trailing/intra whitespace', () => {
    expect(parseSlash('  /agent   draft  ')).toEqual({
      kind: 'agent', runnerType: 'react', task: 'draft',
    });
  });

  test('preserves multi-line task body (shift+enter content)', () => {
    expect(parseSlash('/agent line1\nline2')).toEqual({
      kind: 'agent', runnerType: 'react', task: 'line1\nline2',
    });
  });

  test('/persona — bare clears, with slug binds', () => {
    expect(parseSlash('/persona')).toEqual({ kind: 'persona', slug: '' });
    expect(parseSlash('/persona researcher')).toEqual({ kind: 'persona', slug: 'researcher' });
  });

  test('/default and /clear collapse to the clear kind', () => {
    expect(parseSlash('/default')).toEqual({ kind: 'clear' });
    expect(parseSlash('/clear')).toEqual({ kind: 'clear' });
  });

  test('unknown commands report the word so the UI can hint', () => {
    expect(parseSlash('/agnet draft')).toEqual({ kind: 'unknown', word: 'agnet' });
    expect(parseSlash('/foo')).toEqual({ kind: 'unknown', word: 'foo' });
  });
});

describe('slugify', () => {
  test('normalises names to lowercase-kebab', () => {
    expect(slugify('Code Reviewer')).toBe('code-reviewer');
  });
  test('trims surrounding whitespace', () => {
    expect(slugify('  Editor  ')).toBe('editor');
  });
  test('strips punctuation, collapses runs of separators', () => {
    expect(slugify("Bob's Persona!")).toBe('bob-s-persona');
  });
  test('empty in, empty out', () => {
    expect(slugify('')).toBe('');
  });
});

describe('SLASH_COMMANDS catalog', () => {
  test('contains at least the four built-in command kinds', () => {
    expect(SLASH_COMMANDS.length).toBeGreaterThanOrEqual(4);
  });
  test('every entry starts with `/` and carries a description', () => {
    for (const c of SLASH_COMMANDS) {
      expect(c.command.startsWith('/')).toBe(true);
      expect(c.description.length).toBeGreaterThan(0);
    }
  });
});
