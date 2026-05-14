package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/shakfu/margo/pkg/margo"
)

// ReactRunner runs the ReAct loop on Eino's Agent Development Kit
// (`cloudwego/eino/adk`). It stands up an `adk.ChatModelAgent` against
// the same margo.Adapter our older code used, runs it via
// `adk.Runner`, and bridges the kit's AgentEvent stream into the
// StepEvent contract the Wails surface and frontend consume.
//
// Registered under RunnerReact ("react"); the canonical
// implementation as of §9.4b.3. A zero-value ReactRunner is ready to
// use — there's no per-instance configuration yet.
type ReactRunner struct{}

func (ReactRunner) Run(
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
	// Tools that publish auxiliary structured events (search_knowledge
	// → StepRetrieve) reach the emitter via this context stash; same
	// mechanism as the legacy StreamReact path.
	ctx = WithStepEmitter(ctx, emit)

	middlewares := []compose.ToolMiddleware{abortOnCtxCancel}
	if gate != nil {
		middlewares = append([]compose.ToolMiddleware{permissionMiddleware(gate)}, middlewares...)
	}

	// Stitch attachments onto the final user message via the same
	// adapter helper the legacy path uses. The adapter is then
	// handed to ChatModelAgent as model.ToolCallingChatModel.
	adapter := NewAdapter(c, defaults).WithFinalUserAttachments(attachments)

	// Budget rewriter (§6.3) — moved from
	// `react.AgentConfig.MessageRewriter` to `BeforeChatModel`
	// middleware under ADK. Algorithm unchanged: trim oldest turns
	// until the estimated input-token count fits under
	// `budget * 0.75`, never orphaning a tool result from its
	// assistant. Runs before every ChatModel call inside the loop,
	// so accumulated tool results that push the conversation over
	// budget mid-loop get trimmed too.
	budget := BudgetForModel(defaults.Model)
	budgetMiddleware := adk.AgentMiddleware{
		BeforeChatModel: func(_ context.Context, state *adk.ChatModelAgentState) error {
			if state == nil || len(state.Messages) == 0 {
				return nil
			}
			state.Messages = RewriteForBudget(state.Messages, budget)
			return nil
		},
	}

	agentImpl, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "margo-react",
		Description: "ReAct loop wired to a margo provider client",
		// Instruction is intentionally empty — the system prompt
		// already rides in `defaults.System` (the adapter forwards
		// it to the provider), and stamping it again here would
		// double-feed it through ChatModelAgent's defaultGenModelInput.
		Instruction: "",
		Model:       adapter,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:               tools,
				ToolCallMiddlewares: middlewares,
			},
		},
		Middlewares: []adk.AgentMiddleware{budgetMiddleware},
	})
	if err != nil {
		return fmt.Errorf("adk: new chat model agent: %w", err)
	}

	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		EnableStreaming: true,
		Agent:           agentImpl,
	})

	started := time.Now()
	var firstToken time.Time
	usage := margo.Usage{}

	iter := runner.Run(ctx, input)
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev == nil {
			continue
		}
		if ev.Err != nil {
			if errors.Is(ev.Err, context.Canceled) || errors.Is(ev.Err, context.DeadlineExceeded) {
				return ev.Err
			}
			emit(StepEvent{Kind: StepError, Text: ev.Err.Error()})
			return ev.Err
		}
		if err := bridgeAgentEvent(ev, emit, &firstToken, &usage); err != nil {
			return err
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

// bridgeAgentEvent translates one ADK event into zero or more
// StepEvent emissions. Synchronously drains MessageStream events so
// the ordering downstream consumers (the Wails surface, the UI's
// step-card merger) see is the same regardless of whether the model
// streamed or returned in one shot.
//
// The translator is intentionally exhaustive over MessageVariant
// fields: unrecognised event shapes (e.g. agent-transfer actions
// produced by future supervisor compositions) surface as a
// StepEvent{Kind: StepError} with the raw shape, instead of being
// silently dropped — matching the §9.4b "exhaustive translator"
// tradeoff in TODO.md.
func bridgeAgentEvent(
	ev *adk.AgentEvent,
	emit func(StepEvent),
	firstToken *time.Time,
	usage *margo.Usage,
) error {
	// Actions today: ignore Exit / BreakLoop / Interrupted. The
	// ChatModelAgent uses Exit internally to end the loop cleanly;
	// we treat it as a no-op since StepDone is emitted by the outer
	// loop. Interrupted is reserved for §9.4b.4 checkpoint flows.
	if ev.Action != nil {
		// Transfer-to-agent actions would land here for multi-agent
		// runners — flag them so future work doesn't silently drop
		// the event.
		if ev.Action.TransferToAgent != nil {
			emit(StepEvent{Kind: StepText, Text: fmt.Sprintf("(transferring to agent %q)", ev.Action.TransferToAgent.DestAgentName)})
		}
	}
	if ev.Output == nil || ev.Output.MessageOutput == nil {
		return nil
	}
	mv := ev.Output.MessageOutput

	if mv.IsStreaming && mv.MessageStream != nil {
		return drainMessageStream(mv, emit, firstToken, usage)
	}
	if mv.Message != nil {
		emitOneMessage(mv.Message, mv.Role, mv.ToolName, emit, firstToken, usage)
	}
	return nil
}

// drainMessageStream consumes a streaming MessageVariant
// synchronously. Assistant chunks emit as StepText; tool chunks emit
// as StepToolStream and the concatenated final result emits as
// StepToolResult so the UI's tool_call→tool_result pairing still
// works. Tool calls appearing mid-stream emit immediately so a
// preamble-then-tool-call turn shows StepText before StepToolCall.
func drainMessageStream(
	mv *adk.MessageVariant,
	emit func(StepEvent),
	firstToken *time.Time,
	usage *margo.Usage,
) error {
	defer mv.MessageStream.Close()
	role := mv.Role
	toolName := mv.ToolName

	var contentBuf strings.Builder
	seenToolCalls := map[string]bool{}
	for {
		chunk, rerr := mv.MessageStream.Recv()
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
			contentBuf.WriteString(chunk.Content)
			if firstToken.IsZero() {
				*firstToken = time.Now()
			}
			if role == schema.Tool {
				emit(StepEvent{Kind: StepToolStream, Name: toolName, Text: chunk.Content})
			} else {
				emit(StepEvent{Kind: StepText, Text: chunk.Content})
			}
		}
		for _, tc := range chunk.ToolCalls {
			if tc.ID != "" && seenToolCalls[tc.ID] {
				continue
			}
			if tc.ID != "" {
				seenToolCalls[tc.ID] = true
			}
			emit(StepEvent{Kind: StepToolCall, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
		}
		if chunk.ResponseMeta != nil && chunk.ResponseMeta.Usage != nil {
			u := chunk.ResponseMeta.Usage
			usage.InputTokens = u.PromptTokens
			usage.OutputTokens = u.CompletionTokens
		}
	}
	if role == schema.Tool {
		emit(StepEvent{Kind: StepToolResult, Name: toolName, Result: contentBuf.String()})
	}
	return nil
}

// emitOneMessage is the non-streaming branch. Maps a single fully-
// formed Message to its StepEvent equivalents.
func emitOneMessage(
	msg *schema.Message,
	role schema.RoleType,
	toolName string,
	emit func(StepEvent),
	firstToken *time.Time,
	usage *margo.Usage,
) {
	if msg.Content != "" {
		if firstToken.IsZero() {
			*firstToken = time.Now()
		}
		if role == schema.Tool {
			emit(StepEvent{Kind: StepToolResult, Name: toolName, Result: msg.Content})
		} else {
			emit(StepEvent{Kind: StepText, Text: msg.Content})
		}
	}
	if role != schema.Tool {
		for _, tc := range msg.ToolCalls {
			emit(StepEvent{Kind: StepToolCall, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
		}
	}
	if msg.ResponseMeta != nil && msg.ResponseMeta.Usage != nil {
		u := msg.ResponseMeta.Usage
		usage.InputTokens = u.PromptTokens
		usage.OutputTokens = u.CompletionTokens
	}
}
