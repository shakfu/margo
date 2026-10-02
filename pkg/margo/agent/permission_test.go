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
	}{
		{"approved", true, 1},
		{"denied", false, 0},
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
			// A denial is returned to the model as the tool result, so
			// neither case may abort the run.
			if err != nil {
				t.Fatalf("run aborted: %v", err)
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

// TestToolFailureReturnsToModel: a denied or failing tool call reaches
// the model as an error result, the run continues to the model's next
// turn, and the UI's result step is flagged IsError. Before this, the
// error aborted the whole run.
func TestToolFailureReturnsToModel(t *testing.T) {
	writes, _ := toolutils.InferTool("writes", "Writes.", func(ctx context.Context, _ struct{}) (string, error) {
		return "wrote", nil
	})
	fails, _ := toolutils.InferTool("fails", "Fails.", func(ctx context.Context, _ struct{}) (string, error) {
		return "", errors.New("disk on fire")
	})
	streams, _ := toolutils.InferStreamTool("streams", "Streams.", func(ctx context.Context, _ struct{}) (*schema.StreamReader[string], error) {
		return schema.StreamReaderFromArray([]string{"a", "b"}), nil
	})
	deny := func(context.Context, string, string) (bool, error) { return false, nil }

	cases := []struct {
		name, toolName string
		tool           tool.BaseTool
		gate           PermissionGate
		wantText       string
	}{
		{"denied", "writes", writes, deny, "denied permission"},
		{"tool error", "fails", fails, nil, "disk on fire"},
		{"denied streaming", "streams", streams, deny, "denied permission"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &scriptedClient{turns: [][]margo.Chunk{
				{{Kind: margo.ChunkToolCall, ToolCall: &margo.ToolCall{ID: "c1", Name: tc.toolName, Arguments: `{}`}}},
				{{Kind: margo.ChunkText, Text: "I could not do that."}},
			}}
			var (
				mu     sync.Mutex
				result *StepEvent
				done   bool
			)
			err := ReactRunner{}.Run(context.Background(), client, margo.Request{Model: "test"},
				[]tool.BaseTool{tc.tool}, []*schema.Message{{Role: schema.User, Content: "go"}}, nil, tc.gate,
				func(ev StepEvent) {
					mu.Lock()
					defer mu.Unlock()
					switch ev.Kind {
					case StepToolResult:
						e := ev
						result = &e
					case StepDone:
						done = true
					}
				})
			if err != nil {
				t.Fatalf("run aborted: %v", err)
			}
			if !done {
				t.Error("no StepDone")
			}
			if result == nil || !result.IsError || !strings.Contains(result.Result, tc.wantText) {
				t.Errorf("tool result = %+v, want IsError with %q", result, tc.wantText)
			}
			if len(client.reqs) != 2 {
				t.Fatalf("model called %d times, want 2", len(client.reqs))
			}
			var sawTool bool
			for _, m := range client.reqs[1].Messages {
				if m.Role == margo.RoleTool && m.ToolCallID == "c1" && strings.Contains(m.Content, tc.wantText) {
					sawTool = true
				}
			}
			if !sawTool {
				t.Errorf("second model call lacks the error result: %+v", client.reqs[1].Messages)
			}
		})
	}
}

// TestToolSuccessIsNotFlagged guards the other direction: a normal
// result must not be marked as an error.
func TestToolSuccessIsNotFlagged(t *testing.T) {
	writes, _ := toolutils.InferTool("writes", "Writes.", func(ctx context.Context, _ struct{}) (string, error) {
		return "wrote", nil
	})
	client := &scriptedClient{turns: [][]margo.Chunk{
		{{Kind: margo.ChunkToolCall, ToolCall: &margo.ToolCall{ID: "c1", Name: "writes", Arguments: `{}`}}},
		{{Kind: margo.ChunkText, Text: "done"}},
	}}
	var result *StepEvent
	err := ReactRunner{}.Run(context.Background(), client, margo.Request{Model: "test"},
		[]tool.BaseTool{writes}, []*schema.Message{{Role: schema.User, Content: "go"}}, nil, nil,
		func(ev StepEvent) {
			if ev.Kind == StepToolResult {
				e := ev
				result = &e
			}
		})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result == nil || result.IsError || result.Result != "wrote" {
		t.Errorf("tool result = %+v, want unflagged \"wrote\"", result)
	}
}

// TestStreamingToolFailsMidStream: a streaming tool that errors after
// some output ends with the error text. The model and the UI see the
// partial output and the failure, and the run continues.
func TestStreamingToolFailsMidStream(t *testing.T) {
	breaks, _ := toolutils.InferStreamTool("breaks", "Breaks.", func(ctx context.Context, _ struct{}) (*schema.StreamReader[string], error) {
		r, w := schema.Pipe[string](3)
		w.Send("a", nil)
		w.Send("b", nil)
		w.Send("", errors.New("pipe broke"))
		w.Close()
		return r, nil
	})
	client := &scriptedClient{turns: [][]margo.Chunk{
		{{Kind: margo.ChunkToolCall, ToolCall: &margo.ToolCall{ID: "c1", Name: "breaks", Arguments: `{}`}}},
		{{Kind: margo.ChunkText, Text: "partial result noted"}},
	}}
	var result *StepEvent
	err := ReactRunner{}.Run(context.Background(), client, margo.Request{Model: "test"},
		[]tool.BaseTool{breaks}, []*schema.Message{{Role: schema.User, Content: "go"}}, nil, nil,
		func(ev StepEvent) {
			if ev.Kind == StepToolResult {
				e := ev
				result = &e
			}
		})
	if err != nil {
		t.Fatalf("run aborted: %v", err)
	}
	if result == nil || !result.IsError || !strings.HasPrefix(result.Result, "ab") || !strings.Contains(result.Result, "pipe broke") {
		t.Errorf("tool result = %+v, want IsError with partial output and the error", result)
	}
	if len(client.reqs) != 2 {
		t.Fatalf("model called %d times, want 2", len(client.reqs))
	}
	var sawTool bool
	for _, m := range client.reqs[1].Messages {
		if m.Role == margo.RoleTool && strings.Contains(m.Content, "pipe broke") {
			sawTool = true
		}
	}
	if !sawTool {
		t.Errorf("second model call lacks the error: %+v", client.reqs[1].Messages)
	}
}
