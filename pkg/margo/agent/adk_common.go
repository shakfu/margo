package agent

import (
	"context"
	"errors"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/shakfu/margo/pkg/margo"
)

// runSetup is the shared prologue every ADK-backed runner needs before
// it can assemble its agents: a non-nil emitter, a context carrying that
// emitter for tools to publish through, the tool middleware stack, and
// the model adapter with attachments stamped onto the final user turn.
type runSetup struct {
	ctx         context.Context
	emit        func(StepEvent)
	adapter     *Adapter
	middlewares []compose.ToolMiddleware
}

// prepareRun builds the pieces common to ReactRunner, PlanExecuteRunner
// and WorkflowRunner. The three differ only in which adk.Agent they
// assemble from these; everything before and after was identical.
func prepareRun(
	ctx context.Context,
	c margo.Client,
	defaults margo.Request,
	attachments []margo.Part,
	gate PermissionGate,
	emit func(StepEvent),
) runSetup {
	if emit == nil {
		emit = func(StepEvent) {}
	}
	// Tools that publish auxiliary structured events (search_knowledge
	// -> StepRetrieve) reach the emitter via this context stash.
	ctx = WithStepEmitter(ctx, emit)

	middlewares := []compose.ToolMiddleware{abortOnCtxCancel}
	if gate != nil {
		middlewares = append([]compose.ToolMiddleware{permissionMiddleware(gate)}, middlewares...)
	}

	return runSetup{
		ctx:         ctx,
		emit:        emit,
		adapter:     NewAdapter(c, defaults).WithFinalUserAttachments(attachments),
		middlewares: middlewares,
	}
}

// toolsConfig wraps a tool slice with the run's middleware. An empty
// slice yields a zero ToolsConfig, which is how a stage declares "no
// tools" (the workflow runner's critic and refiner).
func (s runSetup) toolsConfig(tools []tool.BaseTool) adk.ToolsConfig {
	if len(tools) == 0 {
		return adk.ToolsConfig{}
	}
	return adk.ToolsConfig{
		ToolsNodeConfig: compose.ToolsNodeConfig{
			Tools:               tools,
			ToolCallMiddlewares: s.middlewares,
		},
	}
}

// runADKAgent drives an assembled agent to completion, bridging its
// AgentEvent stream into StepEvents and emitting the closing StepDone
// with wall-clock timings.
//
// Cancellation returns the context error without emitting StepError:
// the user pressed stop, which is not a failure to report back to them.
func runADKAgent(ctx context.Context, entry adk.Agent, input []*schema.Message, emit func(StepEvent)) error {
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		EnableStreaming: true,
		Agent:           entry,
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

	usage.TotalMs = time.Since(started).Milliseconds()
	if !firstToken.IsZero() {
		usage.FirstTokenMs = firstToken.Sub(started).Milliseconds()
	}
	u := usage
	emit(StepEvent{Kind: StepDone, Usage: &u})
	return nil
}
