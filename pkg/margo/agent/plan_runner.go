package agent

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk/prebuilt/planexecute"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/shakfu/margo/pkg/margo"
)

// PlanExecuteRunner runs the plan-then-execute control loop.
//
// Three sub-agents cooperate via `planexecute.New`: the **planner**
// (a tool-calling model that emits a structured Plan via the
// PlanTool), the **executor** (a tool-using model that handles each
// plan step against the workspace's enabled tools), and the
// **replanner** (a tool-calling model that decides after each
// executor pass whether to update the plan and continue or finalise
// a response). All three sub-agents share the same margo.Adapter —
// our model wrapper is itself a `model.ToolCallingChatModel`, so
// each role gets the same provider client and the kit calls
// WithTools per-role to bind whichever tools that role needs.
//
// Registered as RunnerPlan ("plan"); backs `/agent-plan <task>`.
// AgentEvent → StepEvent translation reuses bridgeAgentEvent from
// adk_runner.go; nested events from the executor (tool calls and
// results) surface as the same StepToolCall / StepToolResult /
// StepToolStream the ReAct path emits, so the UI rendering is
// uniform across runners.
type PlanExecuteRunner struct{}

// planMaxIterations bounds the execute → replan loop. Default
// matches `planexecute.Config`'s zero-value behaviour but is set
// explicitly so the cap is documented at the call site.
const planMaxIterations = 10

func (PlanExecuteRunner) Run(
	ctx context.Context,
	c margo.Client,
	defaults margo.Request,
	tools []tool.BaseTool,
	input []*schema.Message,
	attachments []margo.Part,
	gate PermissionGate,
	emit func(StepEvent),
) error {
	run := prepareRun(ctx, c, defaults, attachments, gate, emit)

	// One adapter shared across the three sub-agents. WithTools is
	// non-mutating (returns a fresh ToolCallingChatModel), so each
	// sub-agent binds its own tool surface — PlanTool for the planner
	// and replanner, the workspace's enabled palette for the executor —
	// without interfering with the others.
	planner, err := planexecute.NewPlanner(run.ctx, &planexecute.PlannerConfig{
		ToolCallingChatModel: run.adapter,
	})
	if err != nil {
		return fmt.Errorf("plan-execute: new planner: %w", err)
	}
	executor, err := planexecute.NewExecutor(run.ctx, &planexecute.ExecutorConfig{
		Model:       run.adapter,
		ToolsConfig: run.toolsConfig(tools),
	})
	if err != nil {
		return fmt.Errorf("plan-execute: new executor: %w", err)
	}
	replanner, err := planexecute.NewReplanner(run.ctx, &planexecute.ReplannerConfig{
		ChatModel: run.adapter,
	})
	if err != nil {
		return fmt.Errorf("plan-execute: new replanner: %w", err)
	}

	entry, err := planexecute.New(run.ctx, &planexecute.Config{
		Planner:       planner,
		Executor:      executor,
		Replanner:     replanner,
		MaxIterations: planMaxIterations,
	})
	if err != nil {
		return fmt.Errorf("plan-execute: assemble: %w", err)
	}

	return runADKAgent(run.ctx, entry, input, run.emit)
}
