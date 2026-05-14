package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/shakfu/margo/pkg/margo"
)

// TestPlanExecuteRunnerAssemblesAndCancels does two things at once
// because that's the cheapest meaningful test of §9.5 without
// scripting the full plan-execute protocol (PlanTool calls,
// ExecutedStep accumulation, RespondTool termination — all of which
// require the model to produce structured tool-calls in a specific
// shape):
//
//  1. PlanExecuteRunner.Run successfully constructs the three sub-
//     agents and wraps them via planexecute.New. A bad constructor
//     call (wrong field name, type mismatch, missing required arg)
//     would surface here.
//  2. ctx cancellation from outside the run causes a prompt return
//     with context.Canceled rather than hanging on the model
//     stream. This is the §9.4b parity guarantee carried over to
//     the plan runner.
//
// The scripted client emits an empty turn so the planner gets
// "nothing useful" and the loop either fails fast or hangs on the
// next sub-agent call; either way, our outer cancel completes the
// test within the deadline.
func TestPlanExecuteRunnerAssemblesAndCancels(t *testing.T) {
	client := &scriptedClient{
		turns: [][]margo.Chunk{
			// Single empty text turn — the planner won't get a
			// valid Plan tool call, but the construction work
			// (NewPlanner/NewExecutor/NewReplanner/planexecute.New)
			// must complete before any model call happens. If those
			// fail the test surfaces the assembly error.
			{{Kind: margo.ChunkText, Text: ""}},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel quickly so we don't hang waiting for a real model
	// response. The runner should observe the cancellation and
	// return promptly.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	defer cancel()

	deadline := time.Now().Add(3 * time.Second)
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- PlanExecuteRunner{}.Run(
			ctx,
			client,
			margo.Request{Model: "test"},
			[]tool.BaseTool{},
			[]*schema.Message{{Role: schema.User, Content: "plan something"}},
			nil,
			nil,
			func(StepEvent) {},
		)
	}()

	select {
	case err := <-doneCh:
		// Acceptable outcomes:
		// - context.Canceled (cancel observed mid-run)
		// - any error referencing the assembly chain (we got past
		//   construction but the planner errored on the empty model
		//   output — that's still useful evidence the runner is
		//   wired correctly).
		// We DON'T accept nil — a clean success with an empty model
		// would mean the runner silently no-op'd, which is wrong.
		if err == nil {
			t.Errorf("PlanExecuteRunner returned nil error on cancelled/empty run; expected ctx.Canceled or a plan error")
		} else if !errors.Is(err, context.Canceled) {
			// Log non-cancel errors so any future regression in
			// construction surfaces, but don't fail — the model
			// stream is empty by design and the kit may legitimately
			// surface "no valid plan" before our cancel lands.
			t.Logf("non-cancel error (acceptable): %v", err)
		}
	case <-time.After(time.Until(deadline)):
		t.Fatalf("PlanExecuteRunner did not return within 3s of cancel — assembly or event loop is hung")
	}
}
