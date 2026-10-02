package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/shakfu/margo/pkg/margo"
)

// big returns a string sized so estimateTokens reports ~tokens.
func big(tokens int) string {
	return strings.Repeat("x", tokens*4)
}

func TestRewriteForBudgetUnderThreshold(t *testing.T) {
	msgs := []*schema.Message{
		{Role: schema.System, Content: "sys"},
		{Role: schema.User, Content: "hi"},
		{Role: schema.Assistant, Content: "hello"},
	}
	out := RewriteForBudget(msgs, 10_000)
	if len(out) != len(msgs) {
		t.Fatalf("got %d msgs, want %d (no trim expected under threshold)", len(out), len(msgs))
	}
}

func TestRewriteForBudgetDropsOldestTurn(t *testing.T) {
	// budget = 1000 → threshold = 750. Each user/assistant pair below is
	// ~400 tokens. Three pairs = 1200; should drop oldest pair to fit.
	msgs := []*schema.Message{
		{Role: schema.System, Content: "sys"},
		{Role: schema.User, Content: big(200)},
		{Role: schema.Assistant, Content: big(200)},
		{Role: schema.User, Content: big(200)},
		{Role: schema.Assistant, Content: big(200)},
		{Role: schema.User, Content: big(200)},
		{Role: schema.Assistant, Content: big(200)},
	}
	out := RewriteForBudget(msgs, 1000)
	if len(out) >= len(msgs) {
		t.Fatalf("expected trim; got len=%d", len(out))
	}
	if out[0].Role != schema.System {
		t.Errorf("system not preserved")
	}
	// Last turn (the final assistant reply) must remain.
	if out[len(out)-1].Role != schema.Assistant {
		t.Errorf("final turn not preserved")
	}
}

func TestRewriteForBudgetKeepsToolGluedToAssistant(t *testing.T) {
	// Trim must drop (assistant + its tool result) atomically — never
	// strand a tool message without its tool_call counterpart.
	msgs := []*schema.Message{
		{Role: schema.System, Content: "sys"},
		{Role: schema.User, Content: big(300)},
		{Role: schema.Assistant, Content: big(100), ToolCalls: []schema.ToolCall{{ID: "t1"}}},
		{Role: schema.Tool, Content: big(300), ToolCallID: "t1"},
		{Role: schema.Assistant, Content: big(100)},
		{Role: schema.User, Content: big(100)},
		{Role: schema.Assistant, Content: big(100)},
	}
	out := RewriteForBudget(msgs, 1200)

	for i, m := range out {
		if m.Role == schema.Tool {
			// Preceding entry must be the assistant turn that owned it.
			if i == 0 || out[i-1].Role != schema.Assistant {
				t.Errorf("orphan tool message at index %d after trim", i)
			}
		}
	}
}

func TestRewriteForBudgetKeepsFinalTurnEvenIfOversized(t *testing.T) {
	// Single huge user turn larger than budget. Algorithm must still keep
	// it (the user's actual ask) rather than returning empty.
	msgs := []*schema.Message{
		{Role: schema.System, Content: "sys"},
		{Role: schema.User, Content: big(100)},
		{Role: schema.Assistant, Content: big(100)},
		{Role: schema.User, Content: big(2000)}, // way over
	}
	out := RewriteForBudget(msgs, 500)
	if len(out) == 0 {
		t.Fatalf("rewriter must not return empty when final turn is preserved")
	}
	if out[len(out)-1].Content != msgs[len(msgs)-1].Content {
		t.Errorf("final user turn not preserved")
	}
}

func TestRewriteMargoForBudget(t *testing.T) {
	msgs := []margo.Message{
		{Role: margo.RoleUser, Content: big(200)},
		{Role: margo.RoleAssistant, Content: big(200)},
		{Role: margo.RoleUser, Content: big(200)},
		{Role: margo.RoleAssistant, Content: big(200)},
		{Role: margo.RoleUser, Content: big(200)},
	}
	out := RewriteMargoForBudget(msgs, "sysprompt", 1000)
	if len(out) >= len(msgs) {
		t.Fatalf("expected trim; got len=%d", len(out))
	}
	if out[len(out)-1].Role != margo.RoleUser {
		t.Errorf("final user turn not preserved")
	}
}

func TestBudgetForModelFallback(t *testing.T) {
	if got := BudgetForModel("claude-sonnet-4-6"); got != 200_000 {
		t.Errorf("known model: got %d, want 200000", got)
	}
	if got := BudgetForModel("totally-made-up-model-id"); got != defaultContextWindow {
		t.Errorf("unknown model: got %d, want fallback %d", got, defaultContextWindow)
	}
}

// TestEstimateCountsAttachmentBytes is the regression net for the
// budget hole: a turn carrying a large attachment used to estimate at
// roughly four tokens, so the rewriter never trimmed and the provider
// rejected the request instead.
func TestEstimateCountsAttachmentBytes(t *testing.T) {
	text := margo.Message{Role: margo.RoleUser, Content: "describe this"}
	withImage := margo.Message{
		Role:    margo.RoleUser,
		Content: "describe this",
		Parts: []margo.Part{
			{Kind: margo.PartText, Text: "describe this"},
			{Kind: margo.PartImage, MimeType: "image/png", Data: make([]byte, 3*1024*1024)},
		},
	}

	bare := estimateMargoTokens(text)
	loaded := estimateMargoTokens(withImage)
	if loaded <= bare {
		t.Fatalf("attachment added %d tokens to the estimate; want a large increase", loaded-bare)
	}
	if loaded < 1000 {
		t.Errorf("3 MB image estimated at %d tokens, which is implausibly low", loaded)
	}
}

func TestEstimatePartTokens(t *testing.T) {
	// A document is capped at the extraction limit, so a huge PDF
	// cannot dominate the estimate without bound.
	huge := margo.Part{Kind: margo.PartDocument, MimeType: "application/pdf", Data: make([]byte, 50*1024*1024)}
	if got, want := estimatePartTokens(huge), margo.MaxExtractedDocChars/4; got != want {
		t.Errorf("document estimate = %d, want the %d cap", got, want)
	}

	// Even a one-byte image costs the floor.
	tiny := margo.Part{Kind: margo.PartImage, Data: []byte{0x1}}
	if got := estimatePartTokens(tiny); got != minImageTokens {
		t.Errorf("tiny image estimate = %d, want the %d floor", got, minImageTokens)
	}

	if got := estimatePartTokens(margo.Part{Kind: margo.PartText, Text: "abcdefgh"}); got != 2 {
		t.Errorf("text estimate = %d, want 2", got)
	}
}

// With attachments counted, an over-budget history actually trims.
func TestRewriteMargoForBudgetTrimsAttachmentHeavyHistory(t *testing.T) {
	img := func() margo.Message {
		return margo.Message{
			Role:    margo.RoleUser,
			Content: "look",
			Parts: []margo.Part{
				{Kind: margo.PartImage, MimeType: "image/png", Data: make([]byte, 2*1024*1024)},
			},
		}
	}
	msgs := []margo.Message{img(), img(), img(), {Role: margo.RoleUser, Content: "and now?"}}

	got := RewriteMargoForBudget(msgs, "", 8000)
	if len(got) >= len(msgs) {
		t.Fatalf("nothing was trimmed: %d messages in, %d out", len(msgs), len(got))
	}
	// The user's latest ask must survive.
	if got[len(got)-1].Content != "and now?" {
		t.Errorf("final turn was dropped: %+v", got[len(got)-1])
	}
}

// TestReactRunnerAppliesBudget guards the wiring, not the algorithm:
// history over the default 128k budget must reach the model trimmed.
func TestReactRunnerAppliesBudget(t *testing.T) {
	var input []*schema.Message
	for i := 0; i < 3; i++ {
		input = append(input,
			&schema.Message{Role: schema.User, Content: big(20_000)},
			&schema.Message{Role: schema.Assistant, Content: big(20_000)},
		)
	}
	input = append(input, &schema.Message{Role: schema.User, Content: "and now?"})

	client := &scriptedClient{turns: [][]margo.Chunk{{{Kind: margo.ChunkText, Text: "ok"}}}}
	err := ReactRunner{}.Run(context.Background(), client, margo.Request{Model: "unlisted-model"},
		nil, input, nil, nil, func(StepEvent) {})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(client.reqs) != 1 {
		t.Fatalf("got %d model calls, want 1", len(client.reqs))
	}
	got := client.reqs[0].Messages
	if len(got) >= len(input) {
		t.Fatalf("model received %d messages, want fewer than %d (no trim)", len(got), len(input))
	}
	if last := got[len(got)-1]; last.Role != margo.RoleUser || last.Content != "and now?" {
		t.Errorf("final user turn not preserved: %+v", last)
	}
}

// TestWorkflowRunnerAppliesBudget: every stage's model call is trimmed.
// Before the budget moved into the adapter, only the ReAct runner
// trimmed, so each workflow stage sent the full history.
func TestWorkflowRunnerAppliesBudget(t *testing.T) {
	var input []*schema.Message
	for i := 0; i < 3; i++ {
		input = append(input,
			&schema.Message{Role: schema.User, Content: big(20_000)},
			&schema.Message{Role: schema.Assistant, Content: big(20_000)},
		)
	}
	input = append(input, &schema.Message{Role: schema.User, Content: "and now?"})

	client := &scriptedClient{turns: [][]margo.Chunk{
		{{Kind: margo.ChunkText, Text: "draft"}},
		{{Kind: margo.ChunkText, Text: "critique"}},
		{{Kind: margo.ChunkText, Text: "final"}},
	}}
	err := WorkflowRunner{}.Run(context.Background(), client, margo.Request{Model: "unlisted-model"},
		nil, input, nil, nil, func(StepEvent) {})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(client.reqs) != 3 {
		t.Fatalf("got %d model calls, want 3 (one per stage)", len(client.reqs))
	}
	for i, req := range client.reqs {
		bigs := 0
		for _, m := range req.Messages {
			if len(m.Content) >= 20_000*4 {
				bigs++
			}
		}
		// Six 20k-token turns exceed the 96k threshold of the default
		// 128k budget; at most four fit.
		if bigs > 4 {
			t.Errorf("stage %d sent %d oversized messages, want at most 4 (no trim)", i, bigs)
		}
	}
}

// TestAdapterBudgetSurvivesDerivation: ADK and planexecute derive each
// agent's model with WithTools, so the budget must survive it, and every
// runner's adapter must carry one.
func TestAdapterBudgetSurvivesDerivation(t *testing.T) {
	run := prepareRun(context.Background(), &scriptedClient{}, margo.Request{Model: "unlisted-model"}, nil, nil, nil)
	if run.adapter.budget != BudgetForModel("unlisted-model") {
		t.Fatalf("prepareRun budget = %d, want %d", run.adapter.budget, BudgetForModel("unlisted-model"))
	}
	derived, err := run.adapter.WithTools([]*schema.ToolInfo{{Name: "t"}})
	if err != nil {
		t.Fatalf("WithTools: %v", err)
	}
	if got := derived.(*Adapter).budget; got != run.adapter.budget {
		t.Errorf("WithTools dropped the budget: %d, want %d", got, run.adapter.budget)
	}
}
