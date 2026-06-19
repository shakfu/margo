package agent

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/shakfu/margo/pkg/margo"
)

// Runner is the contract every agent control loop satisfies. The ReAct
// loop is one implementation; planned future runners (plan-execute,
// workflow) will be others. Callers route a run to a specific Runner
// instead of hard-coding the loop, which lets the slash-command
// activation model (TODO §9.2) pick between runners per turn without
// touching the surrounding event plumbing.
//
// Arguments and semantics mirror StreamReact: emit is called
// synchronously on event-producing goroutines and must not block; the
// gate may be nil to skip the permission middleware; attachments are
// glued onto the last user message inside the runner via the
// adapter's WithFinalUserAttachments.
type Runner interface {
	Run(
		ctx context.Context,
		c margo.Client,
		defaults margo.Request,
		tools []tool.BaseTool,
		input []*schema.Message,
		attachments []margo.Part,
		gate PermissionGate,
		emit func(StepEvent),
	) error
}

// RunnerType is the stable string identifier the slash-command parser
// and the front-end use to address runners ("react",
// "plan", "workflow"). Strings rather than an enum so the registry
// can grow without recompiling callers.
type RunnerType = string

const (
	// RunnerReact is the default ReAct loop runner. Implemented by
	// ReactRunner (see adk_runner.go); backs the bare
	// `/agent <task>` slash command.
	RunnerReact RunnerType = "react"

	// RunnerPlan is the plan-then-execute runner. Implemented by
	// PlanExecuteRunner (see plan_runner.go); backs the
	// `/agent-plan <task>` slash command. The slash parser strips
	// the `agent-` prefix, leaving "plan" as the runner type.
	RunnerPlan RunnerType = "plan"

	// RunnerWorkflow is the sequential drafter → critic → refiner
	// pipeline (see workflow_runner.go); backs the
	// `/agent-workflow <task>` slash command. §8.3 will re-point
	// this at a user-configurable sub-agent chain; the runner
	// shape stays the same.
	RunnerWorkflow RunnerType = "workflow"
)

// runnerRegistry maps RunnerType strings to factory functions. A factory
// rather than a singleton instance leaves room for per-instance
// configuration on future runners (e.g. plan-execute's max-plan-steps)
// without changing the registry shape.
var (
	runnerRegistryMu sync.RWMutex
	runnerRegistry   = map[RunnerType]func() Runner{
		RunnerReact:    func() Runner { return ReactRunner{} },
		RunnerPlan:     func() Runner { return PlanExecuteRunner{} },
		RunnerWorkflow: func() Runner { return WorkflowRunner{} },
	}
)

// RegisterRunner adds a Runner factory under the given type name.
// Replaces any prior registration; intended for runner packages to
// call from init() (or tests to swap in a fake). Panics on empty
// name to surface programmer errors at startup.
func RegisterRunner(name RunnerType, factory func() Runner) {
	if name == "" {
		panic("agent.RegisterRunner: empty name")
	}
	if factory == nil {
		panic("agent.RegisterRunner: nil factory for " + name)
	}
	runnerRegistryMu.Lock()
	runnerRegistry[name] = factory
	runnerRegistryMu.Unlock()
}

// LookupRunner resolves a RunnerType to a fresh Runner instance. Returns
// an error rather than a zero value on miss so callers can surface a
// clear "unknown runner" message instead of silently falling back to a
// default. Empty name resolves to RunnerReact, matching the slash
// grammar where `/agent <task>` (no type suffix) means ReAct.
func LookupRunner(name RunnerType) (Runner, error) {
	if name == "" {
		name = RunnerReact
	}
	runnerRegistryMu.RLock()
	factory, ok := runnerRegistry[name]
	runnerRegistryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown runner type %q (known: %v)", name, AvailableRunners())
	}
	return factory(), nil
}

// AvailableRunners returns the registered runner types, sorted for
// deterministic output. Used in error messages and (future) the slash
// autocomplete population.
func AvailableRunners() []RunnerType {
	runnerRegistryMu.RLock()
	out := make([]RunnerType, 0, len(runnerRegistry))
	for name := range runnerRegistry {
		out = append(out, name)
	}
	runnerRegistryMu.RUnlock()
	sort.Strings(out)
	return out
}

// RunByType is the convenience entry point for callers that already
// hold all the arguments: it looks up the named runner and invokes
// its Run method. A miss surfaces as an error before any work
// happens, so an unknown runner name doesn't half-start a stream.
func RunByType(
	ctx context.Context,
	name RunnerType,
	c margo.Client,
	defaults margo.Request,
	tools []tool.BaseTool,
	input []*schema.Message,
	attachments []margo.Part,
	gate PermissionGate,
	emit func(StepEvent),
) error {
	r, err := LookupRunner(name)
	if err != nil {
		return err
	}
	return r.Run(ctx, c, defaults, tools, input, attachments, gate, emit)
}
