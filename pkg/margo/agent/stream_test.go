package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/shakfu/margo/pkg/margo"
)

// scriptedClient is a fake margo.Client that yields a pre-recorded sequence
// of streaming chunks per call to Stream. Each invocation consumes one entry
// from `turns`. Generate is unused by the streaming React loop.
type scriptedClient struct {
	mu    sync.Mutex
	turns [][]margo.Chunk
	calls int
}

func (s *scriptedClient) Name() string { return "scripted" }

func (s *scriptedClient) Complete(ctx context.Context, _ margo.Request) (margo.Response, error) {
	return margo.Response{}, nil
}

func (s *scriptedClient) Stream(ctx context.Context, _ margo.Request) (<-chan margo.Chunk, error) {
	s.mu.Lock()
	if s.calls >= len(s.turns) {
		s.mu.Unlock()
		ch := make(chan margo.Chunk)
		close(ch)
		return ch, nil
	}
	chunks := s.turns[s.calls]
	s.calls++
	s.mu.Unlock()

	out := make(chan margo.Chunk, len(chunks))
	for _, c := range chunks {
		out <- c
	}
	close(out)
	return out, nil
}

// TestStreamReactMidLoopTextOrdering verifies that an intermediate model turn's
// text content is emitted as a StepText event BEFORE the same turn's tool-call
// events, and that the final-turn text still arrives via the outer agent
// stream (i.e. is not duplicated by the model callback).
func TestStreamReactMidLoopTextOrdering(t *testing.T) {
	echoTool, err := toolutils.InferTool(
		"echo",
		"Echoes its input back.",
		func(ctx context.Context, in struct {
			Value string `json:"value"`
		}) (string, error) {
			return in.Value, nil
		},
	)
	if err != nil {
		t.Fatalf("InferTool: %v", err)
	}

	client := &scriptedClient{
		turns: [][]margo.Chunk{
			// Turn 1: model produces reasoning text + a tool call.
			{
				{Kind: margo.ChunkText, Text: "Let me echo that. "},
				{Kind: margo.ChunkToolCall, ToolCall: &margo.ToolCall{
					ID:        "call_1",
					Name:      "echo",
					Arguments: `{"value":"hi"}`,
				}},
			},
			// Turn 2: model produces the final answer (no tool calls).
			{
				{Kind: margo.ChunkText, Text: "Done: hi"},
			},
		},
	}

	var (
		mu     sync.Mutex
		events []StepEvent
	)
	emit := func(ev StepEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, ev)
	}

	err = StreamReact(
		context.Background(),
		client,
		margo.Request{Model: "test"},
		[]tool.BaseTool{echoTool},
		[]*schema.Message{{Role: schema.User, Content: "echo hi"}},
		nil,
		nil,
		emit,
	)
	if err != nil {
		t.Fatalf("StreamReact: %v", err)
	}

	// Collect kinds for ordering assertions.
	var kinds []StepKind
	var midText, finalText strings.Builder
	sawTool := false
	for _, ev := range events {
		kinds = append(kinds, ev.Kind)
		switch ev.Kind {
		case StepText:
			if sawTool {
				finalText.WriteString(ev.Text)
			} else {
				midText.WriteString(ev.Text)
			}
		case StepToolCall, StepToolResult:
			sawTool = true
		}
	}

	// Required ordering: at least one StepText, then StepToolCall, then
	// StepToolResult, then at least one StepText, then StepDone.
	wantOrder := []StepKind{StepText, StepToolCall, StepToolResult, StepText, StepDone}
	matched := 0
	for _, k := range kinds {
		if matched < len(wantOrder) && k == wantOrder[matched] {
			matched++
		}
	}
	if matched != len(wantOrder) {
		t.Fatalf("event order mismatch: got %v, want subsequence %v", kinds, wantOrder)
	}

	if got := strings.TrimSpace(midText.String()); got != "Let me echo that." {
		t.Errorf("mid-loop text: got %q, want %q", got, "Let me echo that.")
	}
	if got := finalText.String(); got != "Done: hi" {
		t.Errorf("final text: got %q, want %q", got, "Done: hi")
	}

	// Final-turn dedup guard: the string "Done: hi" should appear in StepText
	// content exactly once across all events, not twice.
	count := 0
	for _, ev := range events {
		if ev.Kind == StepText && strings.Contains(ev.Text, "Done: hi") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("final-turn text emitted %d times, want 1 (dedup regression)", count)
	}
}

// TestStreamReactCancelMidTool verifies that cancelling the parent context
// while a slow tool is executing causes StreamReact to return promptly with
// a context-cancellation error, rather than waiting for the tool to finish.
// Guards the abortOnCtxCancel middleware.
func TestStreamReactCancelMidTool(t *testing.T) {
	toolStarted := make(chan struct{})
	slowTool, err := toolutils.InferTool(
		"slow",
		"Sleeps for a long time, ignoring ctx.",
		func(ctx context.Context, _ struct{}) (string, error) {
			close(toolStarted)
			// Deliberately ignore ctx — simulates a misbehaving tool that
			// won't honor cancellation. The middleware must still let the
			// agent run unwind promptly.
			time.Sleep(5 * time.Second)
			return "never", nil
		},
	)
	if err != nil {
		t.Fatalf("InferTool: %v", err)
	}

	client := &scriptedClient{
		turns: [][]margo.Chunk{
			{
				{Kind: margo.ChunkToolCall, ToolCall: &margo.ToolCall{
					ID:        "call_1",
					Name:      "slow",
					Arguments: `{}`,
				}},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		<-toolStarted
		cancel()
	}()

	emit := func(StepEvent) {}

	deadline := time.Now().Add(2 * time.Second)
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- StreamReact(
			ctx,
			client,
			margo.Request{Model: "test"},
			[]tool.BaseTool{slowTool},
			[]*schema.Message{{Role: schema.User, Content: "go slow"}},
			nil,
			nil,
			emit,
		)
	}()

	select {
	case err := <-doneCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Until(deadline)):
		t.Fatalf("StreamReact did not return within 2s of cancel — abort middleware not honoring ctx")
	}
}
