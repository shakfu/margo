# Agents and Tools

This document covers margo's agent layer: how a run flows from the UI to Eino's Agent Development Kit (ADK), how to add a tool, and how to add a runner. Orchestration runs on [CloudWeGo Eino](https://github.com/cloudwego/eino) ADK (`github.com/cloudwego/eino/adk`). margo supplies the model adapter, the tool and runner registries, the permission gate, and the event bridge to the UI.

"Agent" here means the control loop: the code that picks tools, invokes them, and decides when the run ends. For how agents relate to personas, tools, workspaces and chats, see [`docs/concepts.md`](../concepts.md). For the persona and agent record types, see [`personas_and_agents.md`](personas_and_agents.md).

## Architecture overview

```
Svelte (stream.ts, MessageList.svelte)
   |  StreamAgent(id, provider, ..., toolNames, autoApprove, attachments, runnerType)
   v
app.go  App.StreamAgent ---- emits margo:stream:<id>:{chunk,done,error}
   |
   v
core.Session.StreamAgent     (pkg/margo/core/session.go)
   |  buildTools(names)        builtinTools + MCP tools (core/tools.go)
   |  permissions.gate(...)    PermissionBroker (core/permission.go)
   v
agent.RunByType(runnerType, ...)          (pkg/margo/agent/runner.go)
   |
   v
Runner.Run: prepareRun -> assemble adk.Agent -> runADKAgent
   |           |                                  |
   |           +- Adapter (margo.Client as        +- adk.Runner event loop
   |           |  ToolCallingChatModel; trims     +- bridgeAgentEvent -> StepEvent
   |           |  to the budget, meters usage)    +- meter total -> StepDone.Usage
   |           +- tool middleware:
   |              errors as results, permission,
   |              abortOnCtxCancel
   v
StepEvent -> core.Event -> Wails payload -> stream.ts -> store/chats.ts
```

Four layers:

1. **Provider layer (`pkg/margo`, `pkg/margo/providers`).** `margo.Request.Tools` carries tool definitions. Providers translate them to native function calling and return calls as `Response.ToolCalls` or `Chunk{Kind: ChunkToolCall}`. Tool results go back as `Message{Role: RoleTool, ToolCallID, Content}`.

2. **Agent layer (`pkg/margo/agent`).** `Adapter` presents any `margo.Client` as an Eino `model.ToolCallingChatModel`. A `Runner` assembles an ADK agent around it. `runADKAgent` drives the agent and converts its events to `StepEvent`s.

3. **Session layer (`pkg/margo/core`).** `Session.StreamAgent` resolves tool names, builds the permission gate, registers the run for cancellation, calls `agent.RunByType`, and converts `StepEvent` to `core.Event`. The desktop app, `margo-tui` and `margo-cli` all use it.

4. **Desktop surface (`app.go`, `frontend/`).** `App.StreamAgent` emits each `core.Event` on `margo:stream:<id>:chunk`. `frontend/src/lib/stream.ts` dispatches on `payload.kind`.

## Runners

A runner is one control-loop strategy. All three implement `agent.Runner` and share `prepareRun` and `runADKAgent` (`adk_common.go`).

| Type | Slash command | File | ADK construct | Tools |
| ---- | ------------- | ---- | ------------- | ----- |
| `react` (default) | `/agent <task>` | `adk_runner.go` | one `adk.ChatModelAgent` | all enabled |
| `plan` | `/agent-plan <task>` | `plan_runner.go` | `planexecute` planner, executor, replanner; at most 10 iterations | executor only |
| `workflow` | `/agent-workflow <task>` | `workflow_runner.go` | `adk.NewSequentialAgent` over drafter, critic, refiner | drafter only |

`RunByType` resolves the type through `runnerRegistry`. An empty type means `react`. An unknown type returns an error before any work starts.

`prepareRun` does the setup every runner needs:

- stores the emitter in the context, so tools can call `PublishStep`
- builds the tool middleware, outermost first: `toolErrorsAsResults`, `permissionMiddleware(gate)` (when the gate is non-nil), `abortOnCtxCancel`
- creates the `Adapter`, attaches the turn's image and document attachments to the final user message, and sets the context budget (`WithBudget`)

`runADKAgent` runs the agent with `EnableStreaming: true` and passes every event to `bridgeAgentEvent`. It finishes with one `StepDone`.

`agent.StreamReact`, `agent.React`, `agent.Chat` and `agent.ChatStream` (`agent.go`, `stream.go`) are compatibility wrappers. `StreamReact` calls `ReactRunner`. Nothing outside tests calls them (see notes 11.4 in `notes.md`). New code should call `RunByType`.

## Adding a tool

A tool is an Eino `tool.BaseTool`. The model sees the JSON Schema derived from its input type. The runtime decodes the model's JSON arguments and encodes the result.

### 1. Define the tool

Put it in `pkg/margo/agent/tools_<name>.go`. `toolutils.InferTool` derives the schema from the input struct's tags.

```go
// pkg/margo/agent/tools_readfile.go

func ReadFileTool() tool.InvokableTool {
    type args struct {
        Path string `json:"path" jsonschema:"description=Absolute path to read"`
    }
    t, err := toolutils.InferTool(
        "read_file",
        "Read a UTF-8 text file and return its contents. Use when the user asks about a specific file.",
        func(ctx context.Context, in args) (string, error) {
            b, err := os.ReadFile(in.Path)
            if err != nil {
                return "", err
            }
            return string(b), nil
        },
    )
    if err != nil {
        panic(err) // bad reflection on args; fix at dev time
    }
    return t
}
```

### 2. Register it

Add a constructor to `builtinTools` in `pkg/margo/core/tools.go`. The constructor takes the `*Session`, so a tool can read run-time state such as the active workspace's indexer. Stateless tools ignore it.

```go
// pkg/margo/core/tools.go, inside builtinTools
"read_file": func(*Session) tool.BaseTool { return agent.ReadFileTool() },
```

Tools that depend on an external binary register conditionally. `quarto_render` registers only when `agent.QuartoAvailable()` is true at start-up.

`Session.Tools` and `Session.ToolsMetadata` read the registry, so the frontend's tool picker shows the new name without further changes.

### 3. Set its permission policy

Decide both questions in `pkg/margo/agent/permission.go`:

- **Is it read-only?** A tool with no side effects outside the run goes in `ReadOnlyTools` and never prompts. Anything that touches the filesystem, network, shell or persistent state stays out.
- **Can one approval cover the next call?** If the risk is in the arguments rather than the tool's identity, add it to `NoAlwaysApproveTools`. The UI then offers no "Always", and a stored grant is ignored. `quarto_render` is listed because it writes, and can run, model-authored documents.

Do not add a write-capable tool to `ReadOnlyTools` to reduce prompts. The prompt is the trust boundary. "Always" already removes repeat prompts where that is safe.

### 4. Argument schema tags

`InferTool` reads struct tags through `eino-contrib/jsonschema`:

| Tag | Effect |
| --- | ------ |
| `json:"name"` | Field name in the JSON payload. |
| `json:"name,omitempty"` | Field is optional. |
| `jsonschema:"description=..."` | Description shown to the model. |
| `jsonschema:"required"` | Mark as required. |
| `jsonschema:"enum=a,enum=b"` | Restrict to a string enum. |
| `jsonschema:"minimum=0,maximum=100"` | Numeric bounds. |

For `oneOf`, `anyOf`, `$defs` or recursive types, build the schema with `schema.NewParamsOneOfByJSONSchema(...)` and use `toolutils.NewTool`.

### 5. Execution semantics

- **Context.** Honour `ctx`. `abortOnCtxCancel` stops waiting for an invokable tool when the run is cancelled, but the tool's goroutine keeps running until the tool itself checks `ctx`.
- **Errors.** Return an error when the call fails. `toolErrorsAsResults` sends it to the model as the call's result (`Error: ...`), so the model can retry or answer without the tool, and the UI shows the result in red. A denial gets a fixed message that asks the model not to retry. A streaming tool that fails mid-stream keeps its partial output, followed by the error text (`recoverStream`).
- **Result format.** Return a `string` for text. A struct is JSON-encoded, and the model sees that JSON.
- **Side effects.** Tools run with full process privileges. Validate paths and URLs; the model chooses them.

### 6. Streaming tools and structured side events

- **Streaming output.** Implement `tool.StreamableTool`, e.g. with `toolutils.InferStreamTool`. Each chunk arrives as `StepToolStream`, followed by one `StepToolResult` with the concatenated output. The UI appends chunks to the open call (`appendStepStream` in `store/chats.ts`).
- **Structured events.** A tool can call `agent.PublishStep(ctx, ev)` to send the UI more than its text result. `search_knowledge` publishes `StepRetrieve` with `RetrievalHit`s, which render as cards. The model still receives only the text result.

### 7. MCP tools

Tools from MCP servers are not in `builtinTools`. `buildTools` resolves names of the form `mcp:<server>:<tool>` against the MCP manager and wraps them with `mcp.AsEinoTool`. They are never read-only, so every call prompts until the user chooses "Always".

## Adding a runner

A new runner is a new way of arranging ADK agents. Use the existing pieces.

```go
// pkg/margo/agent/review_runner.go

type ReviewRunner struct{}

func (ReviewRunner) Run(
    ctx context.Context, c margo.Client, defaults margo.Request,
    tools []tool.BaseTool, input []*schema.Message, attachments []margo.Part,
    gate PermissionGate, emit func(StepEvent),
) error {
    run := prepareRun(ctx, c, defaults, attachments, gate, emit)
    a, err := adk.NewChatModelAgent(run.ctx, &adk.ChatModelAgentConfig{
        Name:        "reviewer",
        Description: "Reviews code against the workspace conventions.",
        Instruction: reviewPrompt,
        Model:       run.adapter,
        ToolsConfig: run.toolsConfig(tools),
    })
    if err != nil {
        return fmt.Errorf("review: new agent: %w", err)
    }
    return run.runADKAgent(a, input)
}
```

Then:

1. Add a `RunnerType` constant and an entry in `runnerRegistry` (`runner.go`), or call `RegisterRunner` from `init()`.
2. Add `/agent-<type>` to `SLASH_COMMANDS` in `frontend/src/lib/slash.ts` for autocomplete. The parser already routes any `/agent-<type>`.

Rules:

- Always go through `prepareRun`. It installs the error, permission and cancellation middleware. A runner that builds its own `ToolsConfig` skips the permission prompt, and its tool errors abort the run.
- Always finish with `runADKAgent`. It emits `StepDone` with summed usage and handles cancellation.
- Pass `run.toolsConfig(nil)` for a stage that must not call tools. That yields an empty `ToolsConfig`.
- Give every agent `run.adapter` (or a model derived from it with `WithTools`). The adapter carries the context budget; a separately built model does not trim.
- If a run is only the ReAct loop with a different prompt or tool set, do not add a runner. Have the caller pass different `margo.Request` defaults and tools.

## Step-event protocol

`agent.StepEvent` (`stream.go`) is the contract between runners and front-ends.

| Kind | Meaning | UI rendering |
| ---- | ------- | ------------ |
| `text` | Assistant text delta, from any model call in the run. | Appended to the assistant bubble. |
| `thinking` | Reasoning delta, for providers that report it. | Appended to the collapsible thinking block. |
| `tool_call` | The model called `Name` with `Arguments`. | New step card: `→ name(args)`. |
| `tool_stream` | A chunk of a streaming tool's output. | Appended to the open card. |
| `tool_retrieve` | Structured hits published by a tool. | Hit cards on the open step. |
| `tool_result` | The tool's text result; `IsError` marks a failed or denied call. | Appended to the matching card; red when `IsError`. |
| `permission` | The gate is waiting on the user; carries `PermissionID`. | Card with Approve / Always / Deny. |
| `error` | The run failed; no further events. | Error banner. |
| `done` | The run finished; carries `Usage`. | Closes the bubble; fills the usage footer. |

`permission` comes from the session's gate, not from `bridgeAgentEvent`. The gate emits it directly on the session's event channel.

### Ordering and mid-loop text streaming

`bridgeAgentEvent` drains each model call's stream synchronously. Text and tool calls are emitted in arrival order, so a preamble ("Let me check the time.") appears before the tool call it introduces. Tool calls are de-duplicated by ID within one stream. ADK emits every model call as its own event, so text from intermediate turns streams without any special handling.

### Adding a step kind

1. Add the constant to `StepKind` in `pkg/margo/agent/stream.go` and emit it.
2. Add an `EventKind` in `pkg/margo/core/types.go` and a case in the `StepEvent` switch in `Session.StreamAgent` (`core/session.go`).
3. Extend `AgentStepEvent` in `app.go` if the kind carries new fields.
4. Handle it in `routeChunk` in `frontend/src/lib/stream.ts`, add a helper in `store/chats.ts` if needed, and extend `StepKind` in `store/types.ts`.
5. Render it in `MessageList.svelte`.

`stream.ts` treats an unknown kind as text and appends `payload.text`. A newer backend therefore does not break an older frontend.

## Usage and cost

The adapter counts usage itself (`WithUsageMeter`, set by `prepareRun`). It adds every model call's `margo.Usage`, from `Generate` or the final stream chunk, to one `usageTotal`, and `runADKAgent` puts the total on `StepDone`. Counting at the adapter, rather than from ADK events, includes calls that never surface as events: `planexecute`'s planner and replanner calls were missed that way. Struct copies share the meter, so every agent derived with `WithTools` counts into the same total.

The run's cost is the sum of each call's provider-billed cost (OpenRouter's `usage.cost`), set only when every call reported one. Otherwise it is nil, and the frontend estimates the turn from tokens and catalog rates. Coverage: `TestAgentUsageSumsModelCalls`, `TestAgentUsageCostUnknownIfAnyCallUnreported`, `TestPlanExecuteRunnerCountsEveryModelCall`.

## Cancellation

`Session.StreamAgent` registers the run's context under its id; `Session.Cancel(id)` cancels it.

- **Model calls.** The adapter passes `ctx` to `margo.Client.Stream`, which aborts the HTTP request.
- **Invokable tools.** `abortOnCtxCancel` runs each call in a goroutine and returns `ctx.Err()` when the context ends. At most one goroutine leaks per abandoned call, until the tool checks `ctx`.
- **Streamable tools.** `abortOnCtxCancel` checks `ctx` only before the call starts. Cancelling mid-stream depends on the tool.
- **Permission prompts.** The gate selects on `ctx.Done()`, so a pending prompt unblocks on cancel.
- **Reporting.** `runADKAgent` returns the context error without emitting `StepError`. Pressing stop is not a failure.

Coverage: `TestStreamReactCancelMidTool` runs a tool that sleeps for 5 seconds and ignores `ctx`. It asserts that the run returns `context.Canceled` within 2 seconds.

## Context-window management

`pkg/margo/agent/budget.go` trims history to fit the model's context window:

- **Agent runs.** The `Adapter` trims each model call's messages with `RewriteForBudget` before sending (`WithBudget`, set by `prepareRun`). Every runner and every stage is covered, including the `planexecute` agents, which accept no ADK handlers. Tool results that accumulate mid-run are trimmed too. ADK keeps the full history in its state; only what is sent is trimmed. Coverage: `TestReactRunnerAppliesBudget`, `TestWorkflowRunnerAppliesBudget`, and `TestAdapterBudgetSurvivesDerivation` (the budget must survive `WithTools`, which ADK uses to derive each agent's model).
- **Plain chat.** `RewriteMargoForBudget` runs once in `toMargoRequest` (`core/conv.go`). There is no loop.

The algorithm estimates tokens as `len(content)/4` plus a small per-tool-call overhead. There is no real tokenizer, which keeps the binary CGo-free. It drops the oldest turns until the estimate fits under 75% of the budget. A turn is a user or assistant message plus the tool messages that follow it, so a tool result never loses its tool call. The system prompt and the final turn are always kept.

`BudgetForModel` reads the context window through `margo.LookupModel`: the live provider catalog first, then the embedded `models.json`. Unknown models get 128,000 tokens.

## Tool permission prompts

1. `prepareRun` places `permissionMiddleware(gate)` between `toolErrorsAsResults` and `abortOnCtxCancel`. The middleware skips tools in `ReadOnlyTools`. For any other tool it calls the gate; `(false, nil)` returns `ErrPermissionDenied`, which `toolErrorsAsResults` turns into a result for the model.
2. `Session.StreamAgent` builds the gate from its `PermissionBroker` (`core/permission.go`). The gate mints an id, emits a `permission` event and blocks on the decision channel or `ctx.Done()`.
3. The frontend shows Approve / Always / Deny and calls `App.RespondPermission`, which calls `PermissionBroker.Respond`.
4. **Always** approves the tool for the rest of the run. The frontend also adds it to `Settings.autoApproveTools` and sends that list with every later run. Both steps drop tools for which `AllowsAlwaysApprove` is false, including grants stored before a tool joined `NoAlwaysApproveTools`.

Coverage: `permission_test.go` (approve and deny paths, read-only bypass, cancellation while pending, failures returned to the model) and `TestStreamAgentDropsIneligibleAutoApprovals` in `core/session_test.go`.

## Provider parity

All three providers implement tool calling. The wire shapes differ:

| Provider | Tool definition | Tool calls in the response | Tool result sent back |
| -------- | --------------- | -------------------------- | --------------------- |
| OpenAI | `ChatCompletionFunctionTool{shared.FunctionDefinitionParam}` | `Choice.Message.ToolCalls[]` | `sdk.ToolMessage(content, callID)` |
| OpenRouter | `CreateChatFunctionToolChatFunctionToolFunction(...)` (OpenRouter Go SDK) | `ChatChoice.Message.ToolCalls[]` | `CreateChatMessagesTool(ChatToolMessage{ToolCallID: ...})` |
| Anthropic | `ToolUnionParam{OfTool: &ToolParam{InputSchema: ...}}` | `ToolUseBlock` in `msg.Content` | `tool_result` blocks in a `user` message; consecutive `RoleTool` messages must be batched into one Anthropic message |

The conversions live in each provider package (`toSDKTools` / `toAnthropicTool`, `toSDKMessage` / `toAnthropicMessages`). The `Adapter` sees only `margo.Request`, `margo.Response` and `margo.Chunk`, so it needs no provider-specific code.

## Tool choice

`margo.Request.ToolChoice` maps to each provider's native field:

| Value | Meaning |
| ----- | ------- |
| `""` | Provider default (usually `auto`). |
| `"auto"` | The model decides. |
| `"none"` | The model may not call tools. |
| `"required"` | The model must call a tool. Anthropic's equivalent is `any`. |
| any other | The model must call the named tool. |

Nothing above the provider layer sets it. To expose it, add a field to `core.Options` and `ChatOptions` in `app.go`, copy it into the request in `toMargoRequest`, and add a control in the settings panel.

## Anti-patterns

- **Don't put provider quirks in the adapter.** They belong in the provider package. The adapter must stay generic.
- **Don't accept tool implementations from the frontend.** The binding takes tool names; `buildTools` resolves them in Go. The trust boundary stays in Go.
- **Don't bypass the session.** A `cmd/` binary may call `agent.RunByType` directly. Anything in the desktop app goes through `Session.StreamAgent`, so tool resolution, permissions and cancellation behave the same everywhere.
- **Don't describe tool use in the system prompt.** The `Tools` slice controls what the model can call. To steer when it calls a tool, improve the tool's description.
