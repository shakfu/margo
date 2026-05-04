package agent

import (
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
