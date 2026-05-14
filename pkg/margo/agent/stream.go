package agent

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

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
//     final answer.
//   - StepToolCall:   Name is the tool's identifier, Arguments is the raw
//     JSON the model produced for the call.
//   - StepToolStream: Name is the tool's identifier, Text holds an incremental
//     chunk of the tool's streaming output. Emitted only for
//     StreamableTools; followed by a StepToolResult carrying
//     the concatenated output once the stream ends.
//   - StepToolResult: Name is the tool's identifier, Result is the textual
//     output, IsError flags execution failures.
//   - StepRetrieve:   Name is the retrieval tool's identifier, Hits is the
//     structured list of matches. Emitted in addition to
//     the eventual StepToolResult so the UI can render
//     hits as cards rather than parsing a text blob.
//   - StepError:      Text carries the error message; the run will not emit
//     further events.
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

// StreamReact runs a ReAct agent loop and emits step-by-step progress through
// `emit`. Each intermediate model turn's text content is emitted as a single
// StepText event (before its tool calls), each tool invocation triggers a
// StepToolCall + StepToolResult pair (in execution order), and the final
// assistant answer streams as StepText deltas. A final StepDone event closes
// the run.
//
// The `emit` callback runs synchronously on event-producing goroutines —
// it must not block.
//
// As of §9.4b.3 this is a thin compatibility wrapper around
// ReactRunner — the ReAct loop now runs on Eino's Agent Development
// Kit (see adk_runner.go) instead of the legacy `flow/agent/react`
// package. The function signature, event contract, and middleware
// semantics are preserved verbatim, but the underlying
// ChatModelAgent supersedes our hand-rolled callback handlers,
// mid-loop streaming workaround, and custom StreamToolCallChecker.
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
	return ReactRunner{}.Run(ctx, c, defaults, tools, input, attachments, gate, emit)
}
