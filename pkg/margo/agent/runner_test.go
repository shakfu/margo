package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"

	"github.com/shakfu/margo/pkg/margo"
)

func TestLookupRunnerKnown(t *testing.T) {
	r, err := LookupRunner(RunnerReact)
	if err != nil {
		t.Fatalf("LookupRunner(%q): %v", RunnerReact, err)
	}
	if _, ok := r.(ReactRunner); !ok {
		t.Errorf("expected ReactRunner, got %T", r)
	}
}

// TestLookupRunnerPlan guards the §9.5 plan-execute registration.
// /agent-plan in the slash parser routes runnerType="plan", which
// has to resolve to PlanExecuteRunner.
func TestLookupRunnerPlan(t *testing.T) {
	r, err := LookupRunner(RunnerPlan)
	if err != nil {
		t.Fatalf("LookupRunner(%q): %v", RunnerPlan, err)
	}
	if _, ok := r.(PlanExecuteRunner); !ok {
		t.Errorf("expected PlanExecuteRunner, got %T", r)
	}
}

// TestLookupRunnerWorkflow guards the §9.6 sequential-workflow
// registration. /agent-workflow routes runnerType="workflow".
func TestLookupRunnerWorkflow(t *testing.T) {
	r, err := LookupRunner(RunnerWorkflow)
	if err != nil {
		t.Fatalf("LookupRunner(%q): %v", RunnerWorkflow, err)
	}
	if _, ok := r.(WorkflowRunner); !ok {
		t.Errorf("expected WorkflowRunner, got %T", r)
	}
}

func TestLookupRunnerEmptyDefaultsToReact(t *testing.T) {
	// Empty name resolves to the default runner per the slash grammar
	// ("/agent <task>" with no type suffix means ReAct). Critical
	// because the Wails surface will sometimes get an unset string
	// before the slash parser fills it in.
	r, err := LookupRunner("")
	if err != nil {
		t.Fatalf("LookupRunner(\"\"): %v", err)
	}
	if _, ok := r.(ReactRunner); !ok {
		t.Errorf("empty name should resolve to ReactRunner, got %T", r)
	}
}

func TestLookupRunnerUnknown(t *testing.T) {
	_, err := LookupRunner("nope")
	if err == nil {
		t.Fatalf("LookupRunner(unknown) should error")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error should name the bad type: %v", err)
	}
}

func TestAvailableRunnersIncludesReact(t *testing.T) {
	names := AvailableRunners()
	found := false
	for _, n := range names {
		if n == RunnerReact {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("AvailableRunners() should include %q, got %v", RunnerReact, names)
	}
}

// fakeRunner records that it was invoked. Used to confirm the registry
// resolves names to the right factory.
type fakeRunner struct {
	mu     sync.Mutex
	called bool
}

func (f *fakeRunner) Run(_ context.Context, _ margo.Client, _ margo.Request, _ []tool.BaseTool, _ []*schema.Message, _ []margo.Part, _ PermissionGate, _ func(StepEvent)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.called = true
	return nil
}

func TestRegisterRunnerAndDispatch(t *testing.T) {
	f := &fakeRunner{}
	RegisterRunner("fake", func() Runner { return f })

	if err := RunByType(context.Background(), "fake", nil, margo.Request{}, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("RunByType: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.called {
		t.Errorf("fake runner should have been invoked by the registry")
	}
}

func TestRegisterRunnerRejectsEmptyName(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Errorf("RegisterRunner(\"\") should panic")
		}
	}()
	RegisterRunner("", func() Runner { return ReactRunner{} })
}

func TestRegisterRunnerRejectsNilFactory(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Errorf("RegisterRunner with nil factory should panic")
		}
	}()
	RegisterRunner("nilfactory", nil)
}

// TestRunByTypeReactSmoke wires a real scriptedClient through
// RunByType("react", ...) and asserts the same StepDone-terminated
// event sequence as TestStreamReactMidLoopTextOrdering. Guards that
// the registry's ReactRunner factory really does delegate to
// StreamReact unchanged — a regression here would be a silent
// behavior swap on every existing chat.
func TestRunByTypeReactSmoke(t *testing.T) {
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
			{
				{Kind: margo.ChunkToolCall, ToolCall: &margo.ToolCall{
					ID: "call_1", Name: "echo", Arguments: `{"value":"hi"}`,
				}},
			},
			{
				{Kind: margo.ChunkText, Text: "done"},
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

	err = RunByType(
		context.Background(),
		RunnerReact,
		client,
		margo.Request{Model: "test"},
		[]tool.BaseTool{echoTool},
		[]*schema.Message{{Role: schema.User, Content: "echo hi"}},
		nil,
		nil,
		emit,
	)
	if err != nil {
		t.Fatalf("RunByType: %v", err)
	}

	// Required subsequence: at least one tool_call/result pair, then a
	// final text turn, then StepDone. Same shape StreamReact produces.
	want := []StepKind{StepToolCall, StepToolResult, StepText, StepDone}
	matched := 0
	for _, ev := range events {
		if matched < len(want) && ev.Kind == want[matched] {
			matched++
		}
	}
	if matched != len(want) {
		t.Errorf("event order mismatch: got %v, want subsequence %v", kindsOf(events), want)
	}
}

func kindsOf(evs []StepEvent) []StepKind {
	out := make([]StepKind, len(evs))
	for i, e := range evs {
		out[i] = e.Kind
	}
	return out
}

// guard: ensures ReactRunner{} continues to satisfy Runner. A future
// signature drift would catch here at compile time rather than at the
// dispatch call site.
var _ Runner = ReactRunner{}

// confirm StreamReact's "emit may be nil" contract is honoured by the
// registry path too. The runner shouldn't panic when emit is nil; it
// should swap in a no-op the same way StreamReact does.
func TestRunByTypeNilEmitTolerated(t *testing.T) {
	client := &scriptedClient{
		turns: [][]margo.Chunk{
			{{Kind: margo.ChunkText, Text: "ok"}},
		},
	}
	err := RunByType(
		context.Background(),
		RunnerReact,
		client,
		margo.Request{Model: "test"},
		nil,
		[]*schema.Message{{Role: schema.User, Content: "ping"}},
		nil,
		nil,
		nil,
	)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("RunByType with nil emit: %v", err)
	}
}
