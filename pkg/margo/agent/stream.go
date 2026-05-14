package agent

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	flowagent "github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	cbtmpl "github.com/cloudwego/eino/utils/callbacks"

	"github.com/shakfu/margo/pkg/margo"
)

// StepKind classifies a streaming agent event.
type StepKind string

const (
	StepText       StepKind = "text"
	StepToolCall   StepKind = "tool_call"
	StepToolStream StepKind = "tool_stream"
	StepToolResult StepKind = "tool_result"
	StepRetrieve   StepKind = "tool_retrieve"
	StepPermission StepKind = "permission"
	StepError      StepKind = "error"
	StepDone       StepKind = "done"
)

// RetrievalHit is one match surfaced by a knowledge-retrieval tool (e.g.
// search_knowledge) for structured display in the UI. The tool also returns
// its formatted text result through the standard tool_result path so the
// model still sees the chunks; RetrievalHit is for the human reader.
type RetrievalHit struct {
	Path    string  `json:"path"`
	Doc     string  `json:"doc,omitempty"`
	Score   float32 `json:"score"`
	Snippet string  `json:"snippet,omitempty"`
}

// StepEvent is one unit of agent progress, surfaced for UI rendering.
//
// Field semantics by Kind:
//   - StepText:       Text holds the streamed content delta of the agent's
//                     final answer.
//   - StepToolCall:   Name is the tool's identifier, Arguments is the raw
//                     JSON the model produced for the call.
//   - StepToolStream: Name is the tool's identifier, Text holds an incremental
//                     chunk of the tool's streaming output. Emitted only for
//                     StreamableTools; followed by a StepToolResult carrying
//                     the concatenated output once the stream ends.
//   - StepToolResult: Name is the tool's identifier, Result is the textual
//                     output, IsError flags execution failures.
//   - StepRetrieve:   Name is the retrieval tool's identifier, Hits is the
//                     structured list of matches. Emitted in addition to
//                     the eventual StepToolResult so the UI can render
//                     hits as cards rather than parsing a text blob.
//   - StepError:      Text carries the error message; the run will not emit
//                     further events.
//   - StepDone:       The run has finished successfully; Usage is set.
type StepEvent struct {
	Kind         StepKind
	Text         string
	Name         string
	Arguments    string
	Result       string
	IsError      bool
	PermissionID string
	Usage        *margo.Usage
	// Hits is set for StepRetrieve events: the structured list of
	// matches a retrieval tool surfaced. The matching StepToolResult
	// (with the same Name) carries the textual rendering for the model.
	Hits []RetrievalHit
}

// stepEmitterKey is the context key under which StreamReact stashes the
// emit callback so tools running mid-loop can publish auxiliary step events
// (e.g. retrieval hits) without going through the regular text-return path.
type stepEmitterKey struct{}

// WithStepEmitter returns a child context that carries the given emit
// callback. Tools call PublishStep with that context to surface structured
// events (RetrievalHit, future enrichments) to the UI in addition to their
// text return value.
func WithStepEmitter(ctx context.Context, emit func(StepEvent)) context.Context {
	if emit == nil {
		return ctx
	}
	return context.WithValue(ctx, stepEmitterKey{}, emit)
}

// PublishStep emits a step event through whatever emitter the surrounding
// StreamReact run installed. No-op if the context has no emitter (e.g.
// when a tool is invoked outside an agent run from a test). Safe to call
// from any goroutine spawned by a tool.
func PublishStep(ctx context.Context, ev StepEvent) {
	emit, _ := ctx.Value(stepEmitterKey{}).(func(StepEvent))
	if emit == nil {
		return
	}
	emit(ev)
}

// abortOnCtxCancel races each tool invocation against ctx.Done(). When the
// caller cancels (e.g. user hits the cancel button while a slow tool is
// running), the React loop returns ctx.Err() immediately rather than waiting
// for the in-flight tool to finish. The tool's own goroutine keeps running
// until it observes ctx itself; this leaks at most one goroutine per
// abandoned call but unblocks the agent run promptly.
var abortOnCtxCancel = compose.ToolMiddleware{
	Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
		return func(ctx context.Context, in *compose.ToolInput) (*compose.ToolOutput, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			type result struct {
				out *compose.ToolOutput
				err error
			}
			done := make(chan result, 1)
			go func() {
				out, err := next(ctx, in)
				done <- result{out, err}
			}()
			select {
			case r := <-done:
				return r.out, r.err
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	},
	Streamable: func(next compose.StreamableToolEndpoint) compose.StreamableToolEndpoint {
		return func(ctx context.Context, in *compose.ToolInput) (*compose.StreamToolOutput, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return next(ctx, in)
		}
	},
}

// streamHasToolCall scans the entire model output stream for a tool call
// rather than only the first content chunk. Replaces eino's default
// firstChunkStreamToolCallChecker, which classifies "text first, then tool
// call" turns (common with Claude when the model emits a brief preamble
// before invoking a tool) as terminal and ends the ReAct loop prematurely.
func streamHasToolCall(_ context.Context, sr *schema.StreamReader[*schema.Message]) (bool, error) {
	defer sr.Close()
	for {
		msg, err := sr.Recv()
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if msg != nil && len(msg.ToolCalls) > 0 {
			return true, nil
		}
	}
}

// StreamReact runs a ReAct agent loop and emits step-by-step progress through
// `emit`. Each intermediate model turn's text content is emitted as a single
// StepText event (before its tool calls), each tool invocation triggers a
// StepToolCall + StepToolResult pair (in execution order), and the final
// assistant answer streams as StepText deltas. A final StepDone event closes
// the run.
//
// The `emit` callback runs synchronously on event-producing goroutines —
// it must not block.
func StreamReact(
	ctx context.Context,
	c margo.Client,
	defaults margo.Request,
	tools []tool.BaseTool,
	input []*schema.Message,
	attachments []margo.Part,
	gate PermissionGate,
	emit func(StepEvent),
) error {
	if emit == nil {
		emit = func(StepEvent) {}
	}
	// Make the emitter reachable to tools that want to publish structured
	// auxiliary events (e.g. search_knowledge's retrieval hits) on top of
	// their normal text return.
	ctx = WithStepEmitter(ctx, emit)

	middlewares := []compose.ToolMiddleware{abortOnCtxCancel}
	if gate != nil {
		// Permission gate runs before the abort middleware so we don't
		// race a cancellation against a pending user prompt — the gate's
		// own ctx-aware select handles cancel correctly while waiting.
		middlewares = append([]compose.ToolMiddleware{permissionMiddleware(gate)}, middlewares...)
	}

	adapter := NewAdapter(c, defaults).WithFinalUserAttachments(attachments)
	a, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: adapter,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools:               tools,
			ToolCallMiddlewares: middlewares,
		},
		StreamToolCallChecker: streamHasToolCall,
		MessageRewriter:       budgetRewriter(defaults.Model),
	})
	if err != nil {
		return err
	}

	started := time.Now()
	var firstToken time.Time
	emitText := func(text string) {
		if text == "" {
			return
		}
		if firstToken.IsZero() {
			firstToken = time.Now()
		}
		emit(StepEvent{Kind: StepText, Text: text})
	}

	// Mid-loop model turns (those that produce tool calls) stream their text
	// through here so users see the model's reasoning before the tool cards.
	// The final turn's text is delivered through the outer agent stream below;
	// we suppress this handler's emission for it to avoid duplication.
	//
	// Drains synchronously so the StepText event is emitted before the
	// downstream tool node fires its StepToolCall callbacks.
	modelHandler := &cbtmpl.ModelCallbackHandler{
		OnEndWithStreamOutput: func(ctx context.Context, info *callbacks.RunInfo, out *schema.StreamReader[*model.CallbackOutput]) context.Context {
			defer out.Close()
			var content strings.Builder
			hasToolCalls := false
			for {
				chunk, rerr := out.Recv()
				if errors.Is(rerr, io.EOF) {
					break
				}
				if rerr != nil {
					return ctx
				}
				if chunk == nil || chunk.Message == nil {
					continue
				}
				if chunk.Message.Content != "" {
					content.WriteString(chunk.Message.Content)
				}
				if len(chunk.Message.ToolCalls) > 0 {
					hasToolCalls = true
				}
			}
			if hasToolCalls {
				emitText(content.String())
			}
			return ctx
		},
	}

	toolHandler := &cbtmpl.ToolCallbackHandler{
		OnStart: func(ctx context.Context, info *callbacks.RunInfo, in *tool.CallbackInput) context.Context {
			args := ""
			if in != nil {
				args = in.ArgumentsInJSON
			}
			emit(StepEvent{Kind: StepToolCall, Name: info.Name, Arguments: args})
			return ctx
		},
		OnEnd: func(ctx context.Context, info *callbacks.RunInfo, out *tool.CallbackOutput) context.Context {
			result := ""
			if out != nil {
				result = out.Response
			}
			emit(StepEvent{Kind: StepToolResult, Name: info.Name, Result: result})
			return ctx
		},
		// Streamable tools deliver their output as a stream of *tool.CallbackOutput
		// chunks. Forward each chunk as a StepToolStream event for live UI
		// rendering, then emit a single StepToolResult with the concatenated
		// text so downstream merge logic (which pairs a tool_call with its
		// final result) still functions identically to the non-streaming path.
		OnEndWithStreamOutput: func(ctx context.Context, info *callbacks.RunInfo, out *schema.StreamReader[*tool.CallbackOutput]) context.Context {
			defer out.Close()
			var acc strings.Builder
			for {
				chunk, rerr := out.Recv()
				if errors.Is(rerr, io.EOF) {
					break
				}
				if rerr != nil {
					emit(StepEvent{Kind: StepToolResult, Name: info.Name, Result: rerr.Error(), IsError: true})
					return ctx
				}
				if chunk == nil || chunk.Response == "" {
					continue
				}
				acc.WriteString(chunk.Response)
				emit(StepEvent{Kind: StepToolStream, Name: info.Name, Text: chunk.Response})
			}
			emit(StepEvent{Kind: StepToolResult, Name: info.Name, Result: acc.String()})
			return ctx
		},
		OnError: func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
			msg := ""
			if err != nil {
				msg = err.Error()
			}
			emit(StepEvent{Kind: StepToolResult, Name: info.Name, Result: msg, IsError: true})
			return ctx
		},
	}
	cb := react.BuildAgentCallback(modelHandler, toolHandler)

	usage := margo.Usage{}

	reader, err := a.Stream(ctx, input, flowagent.WithComposeOptions(compose.WithCallbacks(cb)))
	if err != nil {
		return err
	}
	defer reader.Close()
	for {
		chunk, rerr := reader.Recv()
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			emit(StepEvent{Kind: StepError, Text: rerr.Error()})
			return rerr
		}
		if chunk == nil {
			continue
		}
		if chunk.Content != "" {
			emitText(chunk.Content)
		}
		if chunk.ResponseMeta != nil && chunk.ResponseMeta.Usage != nil {
			u := chunk.ResponseMeta.Usage
			usage.InputTokens = u.PromptTokens
			usage.OutputTokens = u.CompletionTokens
		}
	}

	now := time.Now()
	usage.TotalMs = now.Sub(started).Milliseconds()
	if !firstToken.IsZero() {
		usage.FirstTokenMs = firstToken.Sub(started).Milliseconds()
	}
	u := usage
	emit(StepEvent{Kind: StepDone, Usage: &u})
	return nil
}
