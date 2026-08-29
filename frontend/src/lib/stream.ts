// Stream-event routing for a chat or agent run.
//
// Extracted from App.svelte's send(): the switch below maps eight event
// kinds onto seven store mutations, and a mismatch between the two shows
// up as a silently wrong transcript rather than an error. In a component
// it was untestable; here it is a pure function over an injected set of
// store actions.
//
// The Go side emits three topics per run:
//
//   margo:stream:<id>:chunk   one StreamChunkEvent or AgentStepEvent
//   margo:stream:<id>:error   a string
//   margo:stream:<id>:done    {usage}
//
// `error` and `done` are both terminal: whichever arrives first
// unsubscribes all three.

import type { RetrievalHit, Usage } from './store';

// StreamPayload is the union the Go side sends on the `:chunk` topic.
// Chat runs use `kind: "text" | "thinking"`; agent runs add the tool and
// permission kinds. Every field beyond `kind` is optional because the
// wire format omits empty values.
export interface StreamPayload {
  kind: string;
  text?: string;
  name?: string;
  arguments?: string;
  result?: string;
  isError?: boolean;
  permissionId?: string;
  chunk?: string;
  hits?: RetrievalHit[];
}

// StreamActions is the set of store mutations a run needs. Injected
// rather than imported so tests can pass spies, and so the routing logic
// carries no dependency on the store module's singletons.
export interface StreamActions {
  appendText(chatId: string, delta: string): void;
  appendThinking(chatId: string, delta: string): void;
  appendStep(chatId: string, step: {
    kind: 'tool_call' | 'permission';
    name: string;
    arguments: string;
    permissionId?: string;
    permissionStatus?: 'pending';
  }): void;
  appendStepStream(chatId: string, name: string, chunk: string): void;
  setStepHits(chatId: string, name: string, hits: RetrievalHit[]): void;
  updateStepResult(chatId: string, name: string, result: string, isError: boolean): void;
  setUsage(chatId: string, usage: Usage): void;
}

// routeChunk applies one `:chunk` payload to the target chat.
//
// Unknown kinds fall through to text. That is deliberate: a payload from
// a newer Go build should append its text rather than vanish, and the
// alternative — dropping it — loses conversation content with no signal.
export function routeChunk(
  chatId: string,
  payload: StreamPayload,
  actions: StreamActions,
): void {
  switch (payload.kind) {
    case 'thinking':
      actions.appendThinking(chatId, payload.text ?? '');
      break;
    case 'tool_call':
      actions.appendStep(chatId, {
        kind: 'tool_call',
        name: payload.name ?? '',
        arguments: payload.arguments ?? '',
      });
      break;
    case 'tool_stream':
      actions.appendStepStream(chatId, payload.name ?? '', payload.chunk ?? '');
      break;
    case 'tool_retrieve':
      actions.setStepHits(chatId, payload.name ?? '', payload.hits ?? []);
      break;
    case 'tool_result':
      actions.updateStepResult(
        chatId,
        payload.name ?? '',
        payload.result ?? '',
        !!payload.isError,
      );
      break;
    case 'permission':
      actions.appendStep(chatId, {
        kind: 'permission',
        name: payload.name ?? '',
        arguments: payload.arguments ?? '',
        permissionId: payload.permissionId,
        permissionStatus: 'pending',
      });
      break;
    case 'text':
    default:
      actions.appendText(chatId, payload.text ?? '');
      break;
  }
}

// StreamHandlers is what subscribeStream needs from the host: the Wails
// event bus, plus the callbacks that own component state (the busy flag,
// the error banner, scroll position).
export interface StreamHandlers {
  on(topic: string, cb: (payload: any) => void): void;
  off(...topics: string[]): void;
  actions: StreamActions;
  onChunk?(): void;
  onError(message: string): void;
  onDone(): void;
}

// subscribeStream wires the three topics for one run and returns a
// teardown function.
//
// Teardown is idempotent and is called on the first terminal event, so a
// late duplicate `:done` after an `:error` cannot resurrect a finished
// run's handlers. The caller also holds the returned function for the
// case where the run never terminates (component destroyed mid-stream).
export function subscribeStream(
  streamId: string,
  chatId: string,
  h: StreamHandlers,
): () => void {
  const base = `margo:stream:${streamId}`;
  const topics = [`${base}:chunk`, `${base}:error`, `${base}:done`];

  let torn = false;
  const teardown = () => {
    if (torn) return;
    torn = true;
    h.off(...topics);
  };

  h.on(topics[0], (payload: StreamPayload) => {
    routeChunk(chatId, payload, h.actions);
    h.onChunk?.();
  });
  h.on(topics[1], (msg: string) => {
    teardown();
    h.onError(msg);
  });
  h.on(topics[2], (payload: { usage: Usage | null }) => {
    if (payload?.usage) h.actions.setUsage(chatId, payload.usage);
    teardown();
    h.onDone();
  });

  return teardown;
}
