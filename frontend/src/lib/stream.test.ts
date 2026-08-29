import { describe, test, expect, vi } from 'vitest';
import { routeChunk, subscribeStream, type StreamActions, type StreamPayload } from './stream';

function spyActions() {
  return {
    appendText: vi.fn(),
    appendThinking: vi.fn(),
    appendStep: vi.fn(),
    appendStepStream: vi.fn(),
    setStepHits: vi.fn(),
    updateStepResult: vi.fn(),
    setUsage: vi.fn(),
  } satisfies StreamActions;
}

describe('routeChunk', () => {
  test('text goes to appendText', () => {
    const a = spyActions();
    routeChunk('c1', { kind: 'text', text: 'hello' }, a);
    expect(a.appendText).toHaveBeenCalledWith('c1', 'hello');
    expect(a.appendThinking).not.toHaveBeenCalled();
  });

  test('thinking is kept separate from the answer body', () => {
    const a = spyActions();
    routeChunk('c1', { kind: 'thinking', text: 'hmm' }, a);
    expect(a.appendThinking).toHaveBeenCalledWith('c1', 'hmm');
    expect(a.appendText).not.toHaveBeenCalled();
  });

  test('tool_call opens a step card', () => {
    const a = spyActions();
    routeChunk('c1', { kind: 'tool_call', name: 'web_fetch', arguments: '{"url":"x"}' }, a);
    expect(a.appendStep).toHaveBeenCalledWith('c1', {
      kind: 'tool_call', name: 'web_fetch', arguments: '{"url":"x"}',
    });
  });

  test('tool_stream appends to the open card, not the message body', () => {
    const a = spyActions();
    routeChunk('c1', { kind: 'tool_stream', name: 'web_fetch', chunk: 'partial' }, a);
    expect(a.appendStepStream).toHaveBeenCalledWith('c1', 'web_fetch', 'partial');
    expect(a.appendText).not.toHaveBeenCalled();
  });

  test('tool_retrieve carries structured hits', () => {
    const a = spyActions();
    const hits = [{ path: 'a.md', score: 0.9 }];
    routeChunk('c1', { kind: 'tool_retrieve', name: 'search_knowledge', hits }, a);
    expect(a.setStepHits).toHaveBeenCalledWith('c1', 'search_knowledge', hits);
  });

  test('tool_result closes the card and carries the error flag', () => {
    const a = spyActions();
    routeChunk('c1', { kind: 'tool_result', name: 'web_fetch', result: 'oops', isError: true }, a);
    expect(a.updateStepResult).toHaveBeenCalledWith('c1', 'web_fetch', 'oops', true);
  });

  test('permission opens a pending card carrying the broker id', () => {
    const a = spyActions();
    routeChunk('c1', { kind: 'permission', name: 'quarto_render', arguments: '{}', permissionId: 'perm-1' }, a);
    expect(a.appendStep).toHaveBeenCalledWith('c1', {
      kind: 'permission',
      name: 'quarto_render',
      arguments: '{}',
      permissionId: 'perm-1',
      permissionStatus: 'pending',
    });
  });

  test('missing fields become empty strings rather than undefined', () => {
    const a = spyActions();
    routeChunk('c1', { kind: 'tool_call' }, a);
    expect(a.appendStep).toHaveBeenCalledWith('c1', { kind: 'tool_call', name: '', arguments: '' });
    routeChunk('c1', { kind: 'text' }, a);
    expect(a.appendText).toHaveBeenCalledWith('c1', '');
  });

  test('an unknown kind falls through to text rather than being dropped', () => {
    const a = spyActions();
    routeChunk('c1', { kind: 'kind_from_a_newer_build', text: 'content' } as StreamPayload, a);
    expect(a.appendText).toHaveBeenCalledWith('c1', 'content');
  });
});

describe('subscribeStream', () => {
  function harness() {
    const handlers = new Map<string, (p: any) => void>();
    const off = vi.fn();
    return {
      handlers,
      off,
      bus: {
        on: (t: string, cb: (p: any) => void) => { handlers.set(t, cb); },
        off: (...topics: string[]) => off(...topics),
      },
      emit: (topic: string, payload: any) => handlers.get(topic)?.(payload),
    };
  }

  test('subscribes to all three topics for the run', () => {
    const h = harness();
    subscribeStream('run1', 'c1', {
      ...h.bus, actions: spyActions(), onError: vi.fn(), onDone: vi.fn(),
    });
    expect([...h.handlers.keys()]).toEqual([
      'margo:stream:run1:chunk',
      'margo:stream:run1:error',
      'margo:stream:run1:done',
    ]);
  });

  test('done reports usage, unsubscribes, and calls onDone', () => {
    const h = harness();
    const actions = spyActions();
    const onDone = vi.fn();
    subscribeStream('run1', 'c1', { ...h.bus, actions, onError: vi.fn(), onDone });

    const usage = { inputTokens: 1, outputTokens: 2, firstTokenMs: 3, totalMs: 4 };
    h.emit('margo:stream:run1:done', { usage });

    expect(actions.setUsage).toHaveBeenCalledWith('c1', usage);
    expect(onDone).toHaveBeenCalled();
    expect(h.off).toHaveBeenCalledWith(
      'margo:stream:run1:chunk',
      'margo:stream:run1:error',
      'margo:stream:run1:done',
    );
  });

  test('a done with no usage still finishes the run', () => {
    const h = harness();
    const actions = spyActions();
    const onDone = vi.fn();
    subscribeStream('run1', 'c1', { ...h.bus, actions, onError: vi.fn(), onDone });
    h.emit('margo:stream:run1:done', { usage: null });
    expect(actions.setUsage).not.toHaveBeenCalled();
    expect(onDone).toHaveBeenCalled();
  });

  test('error unsubscribes and reports the message', () => {
    const h = harness();
    const onError = vi.fn();
    subscribeStream('run1', 'c1', { ...h.bus, actions: spyActions(), onError, onDone: vi.fn() });
    h.emit('margo:stream:run1:error', 'boom');
    expect(onError).toHaveBeenCalledWith('boom');
    expect(h.off).toHaveBeenCalledTimes(1);
  });

  // A late `:done` after an `:error` must not re-run teardown or
  // resurrect a finished run.
  test('teardown is idempotent across terminal events', () => {
    const h = harness();
    const onDone = vi.fn();
    subscribeStream('run1', 'c1', { ...h.bus, actions: spyActions(), onError: vi.fn(), onDone });
    h.emit('margo:stream:run1:error', 'boom');
    h.emit('margo:stream:run1:done', { usage: null });
    expect(h.off).toHaveBeenCalledTimes(1);
  });

  test('the returned teardown unsubscribes a run that never terminates', () => {
    const h = harness();
    const teardown = subscribeStream('run1', 'c1', {
      ...h.bus, actions: spyActions(), onError: vi.fn(), onDone: vi.fn(),
    });
    teardown();
    teardown();
    expect(h.off).toHaveBeenCalledTimes(1);
  });

  test('chunks route to the target chat and fire onChunk', () => {
    const h = harness();
    const actions = spyActions();
    const onChunk = vi.fn();
    subscribeStream('run1', 'chat-42', {
      ...h.bus, actions, onChunk, onError: vi.fn(), onDone: vi.fn(),
    });
    h.emit('margo:stream:run1:chunk', { kind: 'text', text: 'hi' });
    expect(actions.appendText).toHaveBeenCalledWith('chat-42', 'hi');
    expect(onChunk).toHaveBeenCalled();
  });
});
