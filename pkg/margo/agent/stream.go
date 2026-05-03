package agent

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/cloudwego/eino/callbacks"
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
	StepToolResult StepKind = "tool_result"
	StepError      StepKind = "error"
	StepDone       StepKind = "done"
)

// StepEvent is one unit of agent progress, surfaced for UI rendering.
//
// Field semantics by Kind:
//   - StepText:       Text holds the streamed content delta of the agent's
//                     final answer.
//   - StepToolCall:   Name is the tool's identifier, Arguments is the raw
//                     JSON the model produced for the call.
//   - StepToolResult: Name is the tool's identifier, Result is the textual
//                     output, IsError flags execution failures.
//   - StepError:      Text carries the error message; the run will not emit
//                     further events.
//   - StepDone:       The run has finished successfully; Usage is set.
type StepEvent struct {
	Kind      StepKind
	Text      string
	Name      string
	Arguments string
	Result    string
	IsError   bool
	Usage     *margo.Usage
}

// StreamReact runs a ReAct agent loop and emits step-by-step progress through
// `emit`. Each model turn's tool calls trigger StepToolCall + StepToolResult
// pairs (in execution order), and the final assistant answer streams as
// StepText deltas. A final StepDone event closes the run.
//
// The `emit` callback runs synchronously on event-producing goroutines —
// it must not block.
func StreamReact(
	ctx context.Context,
	c margo.Client,
	defaults margo.Request,
	tools []tool.BaseTool,
	input []*schema.Message,
	emit func(StepEvent),
) error {
	if emit == nil {
		emit = func(StepEvent) {}
	}

	adapter := NewAdapter(c, defaults)
	a, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: adapter,
		ToolsConfig:      compose.ToolsNodeConfig{Tools: tools},
	})
	if err != nil {
		return err
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
		OnError: func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
			msg := ""
			if err != nil {
				msg = err.Error()
			}
			emit(StepEvent{Kind: StepToolResult, Name: info.Name, Result: msg, IsError: true})
			return ctx
		},
	}
	cb := react.BuildAgentCallback(nil, toolHandler)

	started := time.Now()
	var firstToken time.Time
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
			if firstToken.IsZero() {
				firstToken = time.Now()
			}
			emit(StepEvent{Kind: StepText, Text: chunk.Content})
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
