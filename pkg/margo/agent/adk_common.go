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
// the model adapter with attachments stamped onto the final user turn and
// each request trimmed to the model's context budget.
type runSetup struct {
	ctx         context.Context
	emit        func(StepEvent)
	adapter     *Adapter
	middlewares []compose.ToolMiddleware
	failed      *failedToolCalls
	meter       *usageTotal
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

	// Outermost first: errors (including a denial) become tool results,
	// then the permission gate, then the cancellation race.
	failed := &failedToolCalls{}
	meter := &usageTotal{}
	middlewares := []compose.ToolMiddleware{toolErrorsAsResults(failed)}
	if gate != nil {
		middlewares = append(middlewares, permissionMiddleware(gate))
	}
	middlewares = append(middlewares, abortOnCtxCancel)

	return runSetup{
		ctx:  ctx,
		emit: emit,
		adapter: NewAdapter(c, defaults).
			WithFinalUserAttachments(attachments).
			WithBudget(BudgetForModel(defaults.Model)).
			WithUsageMeter(meter),
		middlewares: middlewares,
		failed:      failed,
		meter:       meter,
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

// runADKAgent drives an assembled agent to completion, bridging its AgentEvent
// stream into StepEvents and emitting the closing StepDone with
// wall-clock timings.
//
// Cancellation returns the context error without emitting StepError:
// the user pressed stop, which is not a failure to report back to them.
func (s runSetup) runADKAgent(entry adk.Agent, input []*schema.Message) error {
	ctx, emit := s.ctx, s.emit
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		EnableStreaming: true,
		Agent:           entry,
	})

	started := time.Now()
	var firstToken time.Time

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
		if err := bridgeAgentEvent(ev, emit, &firstToken, s.failed); err != nil {
			return err
		}
	}

	usage := s.meter.usage()
	usage.TotalMs = time.Since(started).Milliseconds()
	if !firstToken.IsZero() {
		usage.FirstTokenMs = firstToken.Sub(started).Milliseconds()
	}
	u := usage
	emit(StepEvent{Kind: StepDone, Usage: &u})
	return nil
}
