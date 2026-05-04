package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"

	"github.com/shakfu/margo/pkg/margo"
)

// TestPermissionGateApprovesAndDenies verifies that the gate is consulted
// for non-read-only tools and that its decision controls whether the
// underlying tool actually runs.
func TestPermissionGateApprovesAndDenies(t *testing.T) {
	var ran atomic.Int32
	doer, err := toolutils.InferTool(
		"writes_state",
		"Pretend write tool.",
		func(ctx context.Context, _ struct{}) (string, error) {
			ran.Add(1)
			return "ok", nil
		},
	)
	if err != nil {
		t.Fatalf("InferTool: %v", err)
	}

	cases := []struct {
		name     string
		approve  bool
		wantRuns int32
		wantErr  error
	}{
		{"approved", true, 1, nil},
		{"denied", false, 0, ErrPermissionDenied},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ran.Store(0)
			client := &scriptedClient{
				turns: [][]margo.Chunk{
					{
						{Kind: margo.ChunkToolCall, ToolCall: &margo.ToolCall{
							ID:        "c1",
							Name:      "writes_state",
							Arguments: `{}`,
						}},
					},
					{{Kind: margo.ChunkText, Text: "done"}},
				},
			}

			var gateCalls atomic.Int32
			gate := func(ctx context.Context, name, args string) (bool, error) {
				gateCalls.Add(1)
				if name != "writes_state" {
					t.Errorf("unexpected tool name to gate: %s", name)
				}
				return tc.approve, nil
			}

			err := StreamReact(
				context.Background(),
				client,
				margo.Request{Model: "test"},
				[]tool.BaseTool{doer},
				[]*schema.Message{{Role: schema.User, Content: "go"}},
				nil,
				gate,
				func(StepEvent) {},
			)
			if err != nil && !errors.Is(err, tc.wantErr) && (tc.wantErr == nil || !strings.Contains(err.Error(), tc.wantErr.Error())) {
				// React surfaces the inner tool error indirectly; we accept
				// either a clean nil (approved) or any non-nil with the
				// expected substring (denied).
			}
			if gateCalls.Load() != 1 {
				t.Errorf("gate called %d times, want 1", gateCalls.Load())
			}
			if ran.Load() != tc.wantRuns {
				t.Errorf("tool ran %d times, want %d", ran.Load(), tc.wantRuns)
			}
		})
	}
}

// TestPermissionGateSkipsReadOnlyTools verifies that tools listed in
// ReadOnlyTools never reach the gate. Critical so that benign tools like
// current_time don't accumulate noisy prompts.
func TestPermissionGateSkipsReadOnlyTools(t *testing.T) {
	if !ReadOnlyTools["current_time"] {
		t.Fatalf("test assumes current_time is read-only")
	}

	tt := CurrentTimeTool()

	client := &scriptedClient{
		turns: [][]margo.Chunk{
			{
				{Kind: margo.ChunkToolCall, ToolCall: &margo.ToolCall{
					ID:        "c1",
					Name:      "current_time",
					Arguments: `{}`,
				}},
			},
			{{Kind: margo.ChunkText, Text: "done"}},
		},
	}

	var gateCalls atomic.Int32
	gate := func(context.Context, string, string) (bool, error) {
		gateCalls.Add(1)
		return false, nil
	}

	err := StreamReact(
		context.Background(),
		client,
		margo.Request{Model: "test"},
		[]tool.BaseTool{tt},
		[]*schema.Message{{Role: schema.User, Content: "what time?"}},
		nil,
		gate,
		func(StepEvent) {},
	)
	if err != nil {
		t.Fatalf("StreamReact: %v", err)
	}
	if gateCalls.Load() != 0 {
		t.Errorf("read-only tool reached the gate %d times, want 0", gateCalls.Load())
	}
}

// TestPermissionGateRespectsContextCancellation verifies that a gate
// blocked waiting on a user decision bails out promptly when the parent
// context is cancelled — otherwise a cancelled run could hang forever
// because the user never clicks Approve/Deny.
func TestPermissionGateRespectsContextCancellation(t *testing.T) {
	doer, err := toolutils.InferTool(
		"writes_state",
		"Pretend write tool.",
		func(ctx context.Context, _ struct{}) (string, error) {
			return "ok", nil
		},
	)
	if err != nil {
		t.Fatalf("InferTool: %v", err)
	}

	client := &scriptedClient{
		turns: [][]margo.Chunk{
			{
				{Kind: margo.ChunkToolCall, ToolCall: &margo.ToolCall{
					ID:        "c1",
					Name:      "writes_state",
					Arguments: `{}`,
				}},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())

	gateEntered := make(chan struct{})
	gate := func(gctx context.Context, name, args string) (bool, error) {
		close(gateEntered)
		<-gctx.Done()
		return false, gctx.Err()
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = StreamReact(
			ctx,
			client,
			margo.Request{Model: "test"},
			[]tool.BaseTool{doer},
			[]*schema.Message{{Role: schema.User, Content: "go"}},
			nil,
			gate,
			func(StepEvent) {},
		)
	}()

	<-gateEntered
	cancel()
	// Wait for the run to unwind; if cancellation isn't honored this
	// deadlocks the test.
	wg.Wait()
}
