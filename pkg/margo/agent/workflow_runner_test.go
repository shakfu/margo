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

// TestWorkflowRunnerAssemblesAndCancels mirrors the plan-execute
// assembly test. Confirms the three sub-agents wrap cleanly via
// adk.NewSequentialAgent and that ctx cancellation propagates
// without hanging on a model stream. End-to-end behaviour (drafter
// drafts → critic critiques → refiner polishes) is manual
// verification — scripting three coherent model turns through a
// fake client would be more brittle than useful.
func TestWorkflowRunnerAssemblesAndCancels(t *testing.T) {
	client := &scriptedClient{
		turns: [][]margo.Chunk{
			// One empty turn — enough for the kit to start the
			// first sub-agent before our outer cancel fires.
			{{Kind: margo.ChunkText, Text: ""}},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	defer cancel()

	deadline := time.Now().Add(3 * time.Second)
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- WorkflowRunner{}.Run(
			ctx,
			client,
			margo.Request{Model: "test"},
			[]tool.BaseTool{},
			[]*schema.Message{{Role: schema.User, Content: "write a haiku"}},
			nil,
			nil,
			func(StepEvent) {},
		)
	}()

	select {
	case err := <-doneCh:
		// Like the plan-execute test, we accept ctx.Canceled or any
		// model-stream error from the empty-turn scripted client.
		// We reject nil — that would mean the runner silently
		// no-op'd, which is a bug.
		if err == nil {
			t.Errorf("WorkflowRunner returned nil on a cancelled/empty run; expected ctx.Canceled or a model error")
		} else if !errors.Is(err, context.Canceled) {
			t.Logf("non-cancel error (acceptable): %v", err)
		}
	case <-time.After(time.Until(deadline)):
		t.Fatalf("WorkflowRunner did not return within 3s of cancel — assembly or event loop is hung")
	}
}
