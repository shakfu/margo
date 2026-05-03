# Agents and Tools

This document explains margo's agent layer — how it's wired, how to add a new
tool, and how to introduce a new agent type. The orchestration sits on top of
[CloudWeGo Eino](https://github.com/cloudwego/eino); margo provides the
adapter, the registry, and the UI surface, while Eino handles the graph
execution and the ReAct loop.

## Architecture overview

```
                                +-------------------------+
        UI (App.svelte)         |  agent mode toggle      |
              |                 |  step rendering         |
              v                 +-------------------------+
   StreamAgent(id, ...)  -- Wails binding (app.go)
              |
              v
   agent.StreamReact(ctx, client, defaults, tools, input, emit)
              |
              v
   Eino: react.NewAgent --- ToolCallingChatModel  ---> agent.Adapter
                       \--- ToolsNode (tools)    ---> tool.InvokableTool
                       \--- callbacks            ---> emit StepEvent
```

Three layers:

1. **`pkg/margo` provider layer.** `margo.Client.Stream/Complete` carry a
   `Request.Tools []ToolDef` field; provider implementations (anthropic,
   openai, openrouter) translate this into native function-calling
   parameters and surface assistant tool calls back as
   `Response.ToolCalls` (non-stream) or `Chunk{Kind: ChunkToolCall, ToolCall: *ToolCall}`
   (stream). Tool results travel back as `Message{Role: RoleTool, ToolCallID, Content}`.
2. **`pkg/margo/agent` Eino bridge.** `Adapter` makes any `margo.Client`
   look like an Eino `model.ToolCallingChatModel`. `WithTools` returns a
   new immutable instance with tools captured. `StreamReact` instantiates
   `react.NewAgent` against the adapter, registers tool callbacks, and
   forwards the agent's final-answer stream to a caller-supplied `emit`
   function as `StepEvent` values.
3. **Wails surface (`app.go`) and frontend.** `StreamAgent` resolves
   tool names from a Go-side registry (`builtinTools`), runs
   `agent.StreamReact` in a goroutine, and emits each `StepEvent` over the
   existing `margo:stream:<id>:chunk` event channel as an `AgentStepEvent`
   payload. The frontend dispatches by `payload.kind` and renders steps
   inline above the assistant content.

## Adding a tool

Tools are Eino `tool.InvokableTool` values registered into a Go-side map.
The model sees them via the JSON Schema inferred from your input struct;
the agent loop invokes them by JSON-decoding arguments and JSON-encoding
results.

### 1. Define and register the tool

Tools live in `pkg/margo/agent/agent.go` next to the existing
`CurrentTimeTool`. The fastest path uses `tool/utils.InferTool`, which
generates the parameter JSON Schema from your input struct's tags.

```go
// pkg/margo/agent/agent.go

func ReadFileTool() tool.InvokableTool {
    type args struct {
        Path string `json:"path" jsonschema:"description=Absolute path to read"`
    }
    t, err := toolutils.InferTool(
        "read_file",
        "Read a UTF-8 text file from disk and return its contents. Use this when the user asks about a specific file's content.",
        func(ctx context.Context, in args) (string, error) {
            b, err := os.ReadFile(in.Path)
            if err != nil {
                return "", err
            }
            return string(b), nil
        },
    )
    if err != nil {
        panic(err) // bad reflection on args type — fix at dev time
    }
    return t
}
```

Then add it to the registry in `app.go`:

```go
// app.go

var builtinTools = map[string]func() tool.InvokableTool{
    "current_time": agent.CurrentTimeTool,
    "read_file":    agent.ReadFileTool,
}
```

That's it for the backend. `App.Tools()` already returns the registry's
keys, and `App.StreamAgent` already resolves names against
`builtinTools`. The frontend's `availableTools` will pick up the new
name on next mount, and the agent-mode toggle will pass it through.

### 2. Argument schema conventions

`InferTool` reads the input struct via reflection and JSON-Schema tags
from `eino-contrib/jsonschema`. Useful tags:

| Tag                                              | Effect                              |
| ------------------------------------------------ | ----------------------------------- |
| `json:"name"`                                    | Field name in the JSON payload.     |
| `json:"name,omitempty"`                          | Field is optional.                  |
| `jsonschema:"description=..."`                   | Documented for the model.           |
| `jsonschema:"required"`                          | Force-mark as required.             |
| `jsonschema:"enum=a,enum=b"`                     | Restrict to a string enum.          |
| `jsonschema:"minimum=0,maximum=100"`             | Numeric bounds.                     |

If `InferTool` reflection isn't enough (e.g. you need `oneOf`, `anyOf`,
`$defs`, recursive types), build the schema manually with
`schema.NewParamsOneOfByJSONSchema(...)` and use `tool/utils.NewTool`
instead.

### 3. Tool execution semantics

* **Context.** The first argument to your function is the request `ctx`.
  Honour it: long-running tools should select on `ctx.Done()` so a
  `CancelStream` from the UI surfaces promptly. (See TODO #4 — the
  cancel race today comes from tools that ignore ctx.)
* **Errors.** Returning a non-nil error sends an `error` step event to
  the UI (rendered red) and the model receives no tool result for that
  call. To send the model a soft-failure message it can recover from,
  return an explanatory string with `nil` error.
* **Result format.** The return value is JSON-encoded. For free-form
  text, return a `string`. For structured results, return a struct — the
  model sees the JSON, which is usually fine for tool-use loops.
* **Side effects.** Tools run on the Go side with full process
  privileges. Validate inputs (paths, URLs) defensively when the user is
  driving the model.

### 4. Streaming tools

For tools whose output is naturally a stream (e.g. tailing a log,
running a subprocess), implement `tool.StreamableTool` instead. Use
`tool/utils.InferStreamTool` (analogous to `InferTool`). The agent's
`ToolsNode` will pump chunks back into the conversation. UI rendering of
streaming tool output is not yet implemented in `App.svelte` — see TODO
#5 for the related mid-loop text streaming work.

### 5. Multi-modal results

For tools that return images / files / structured payloads, implement
`tool.EnhancedInvokableTool` (returns `*schema.ToolResult`). The current
margo UI flattens results to text, so multi-modal output requires
extending `AgentStep` and the renderer in `App.svelte` first.

## Adding an agent

The shipped agent is a single ReAct loop (`react.NewAgent`). Eino
supports several other patterns; introduce them as new functions in
`pkg/margo/agent/`.

### Pattern 1 — wrap a different Eino agent

```go
// pkg/margo/agent/host.go

import (
    "github.com/cloudwego/eino/flow/agent/multiagent/host"
)

func StreamHostAgent(
    ctx context.Context,
    c margo.Client,
    defaults margo.Request,
    specialists []*host.Specialist,
    input []*schema.Message,
    emit func(StepEvent),
) error {
    adapter := NewAdapter(c, defaults)
    a, err := host.NewMultiAgent(ctx, &host.MultiAgentConfig{
        Host: host.Host{ChatModel: adapter, /* ... */},
        Specialists: specialists,
    })
    if err != nil { return err }
    // ... same callback wiring + Stream consumption as StreamReact
}
```

Then expose it through a new Wails method on `App` (e.g.
`StreamHost(...)`) following the `StreamAgent` template:

* Resolve any string identifiers (specialist names, tool names) from
  Go-side registries — never trust raw frontend payloads as Go values.
* Track the cancel func in `a.cancels[id]` so `CancelStream(id)` works.
* Translate `StepEvent` → `AgentStepEvent` and emit on
  `margo:stream:<id>:chunk`.

The frontend can either gain a third routing branch in `App.svelte`'s
`send()` (alongside `StreamAgent` / `StreamChat`) or surface agent-type
selection in the settings panel.

### Pattern 2 — custom graph

For workflows that don't map onto a pre-built Eino agent (e.g. a
plan-then-execute loop, parallel sub-agents with a reducer), build a
`compose.Graph` directly:

```go
g := compose.NewGraph[[]*schema.Message, *schema.Message]()
g.AddChatModelNode("planner", plannerAdapter, ...)
g.AddToolsNode("tools", toolsNode, ...)
g.AddLambdaNode("reducer", reducerFn, ...)
g.AddBranch(...)
runner, _ := g.Compile(ctx)
out, _ := runner.Stream(ctx, input)
```

Same callback pattern applies — register a
`utils/callbacks.NewHandlerHelper().ChatModel(...).Tool(...).Handler()`
and forward step events.

### Pattern 3 — same agent, different adapter configuration

Sometimes the new "agent" is just a ReAct loop with a different system
prompt, different tool set, or a different default model. Don't fork
`StreamReact` for these — let the caller supply the appropriate
`margo.Request` defaults and tool slice. Reserve new functions for
genuinely different orchestration shapes.

## Step-event protocol

The contract between agent code and the UI is the `StepEvent` /
`AgentStepEvent` pair:

| StepKind         | UI rendering                                    | Emitted by              |
| ---------------- | ----------------------------------------------- | ----------------------- |
| `StepText`       | Streaming text deltas in the assistant bubble.  | Agent's final-answer stream. |
| `StepToolCall`   | New monospace card: `→ name(args)`.              | `ToolCallbackHandler.OnStart`. |
| `StepToolResult` | Pairs with last open call by name; appends `← result` (or red `← error` if `IsError`). | `ToolCallbackHandler.OnEnd / OnError`. |
| `StepError`      | Red error banner; ends the run.                 | Stream-read errors.     |
| `StepDone`       | Closes the bubble; populates the usage footer.  | End of `StreamReact`.   |

When you add a new step type (e.g. `StepThinking`, `StepBranch`,
`StepRetry`):

1. Extend `StepKind` in `pkg/margo/agent/stream.go`.
2. Extend `AgentStepEvent` in `app.go` and add a case in the
   `StreamAgent` translation switch.
3. Extend `AgentStep` in `frontend/src/lib/store.ts` and add the
   pairing/append helper if needed.
4. Render the new kind in `App.svelte`'s step loop.

Backwards compatibility: the frontend handler dispatches on
`payload.kind` and falls through to text-append on unknown values, so
new kinds added on the backend won't crash older frontends — they just
won't render specially.

## Provider parity

All three first-party providers (`anthropic`, `openai`, `openrouter`)
implement tool calling. The wire shapes differ:

| Provider     | Tool def       | Tool call response          | Tool result back        |
| ------------ | -------------- | --------------------------- | ----------------------- |
| OpenAI/OpenRouter | `ChatCompletionFunctionTool{shared.FunctionDefinitionParam}` | `Choice.Message.ToolCalls[]` | `sdk.ToolMessage(content, callID)` |
| Anthropic    | `ToolUnionParam{OfTool: &ToolParam{InputSchema: ...}}` | `ToolUseBlock` in `msg.Content` | `tool_result` blocks inside a `user` message; consecutive `RoleTool` margo messages **must** batch into one Anthropic message |

The provider files do these conversions (`toSDKTools` / `toAnthropicTool`,
`toSDKMessage` / `toAnthropicMessages`). When adding a fourth provider,
mirror the patterns there. The `agent.Adapter` is provider-agnostic — it
only sees `margo.Request` / `margo.Response` / `margo.Chunk`.

## Tool-choice control

`margo.Request.ToolChoice` is a string forwarded into each provider's
native shape:

| Value       | Meaning                                       |
| ----------- | --------------------------------------------- |
| `""`        | Provider default (usually `auto`).            |
| `"auto"`    | Model decides whether to call any tool.       |
| `"none"`    | Model is forbidden from calling tools.        |
| `"required"`| Model must call at least one tool. (Anthropic uses `any`; same intent.) |
| any other   | Force the model to call the named tool.       |

The agent layer doesn't expose this in the UI yet. To wire it up:
add a string field to `ChatOptions` in `app.go`, plumb through to
`agent.NewAdapter`'s `defaults.ToolChoice`, and surface a select in the
settings panel.

## Anti-patterns

* **Don't fork the adapter per provider.** Provider-specific quirks
  belong in the provider package; the adapter must stay generic.
* **Don't accept tool implementations from the frontend.** The Wails
  binding takes tool *names*; the Go side resolves them through
  `builtinTools`. This keeps the trust boundary at the Go layer.
* **Don't bypass the registry.** Calling `agent.StreamReact` directly
  from a custom `cmd/` is fine, but anything that runs as part of the
  desktop app should go through `App.StreamAgent` so cancellation,
  event plumbing, and tool resolution all work consistently.
* **Don't add "agent mode" to the system prompt.** Tool capability is
  controlled by the `Tools` slice on the request, not by prose. If you
  want the model to behave differently when tools are available, set a
  per-tool description that explains when to call it.

## Related TODOs

* TODO #4 — agent run cancellation race (ctx-aware tool wrappers).
* TODO #5 — mid-loop text streaming via
  `ModelCallbackHandler.OnEndWithStreamOutput`.

Both block making more tool-rich agent experiences feel polished;
worth picking up before adding many new tools that do real work.
