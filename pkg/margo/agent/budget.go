package agent

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/shakfu/margo/pkg/margo"
)

// contextWindows mirrors frontend/src/lib/store.ts::CONTEXT_WINDOWS — the
// per-model budget the UI's context-usage ring uses. Kept in sync by hand
// because the frontend reads it via a TS module and the Go side via this
// map; if you add a model in one place, add it here too.
var contextWindows = map[string]int{
	"claude-haiku-4-5":  200_000,
	"claude-sonnet-4-6": 200_000,
	"claude-opus-4-7":   1_000_000,
	"gpt-5.5":           400_000,
	"gpt-5.5-pro":       400_000,
	"gpt-5.4":           400_000,
	"gpt-5.4-mini":      400_000,
	"gpt-5.4-nano":      400_000,
	"gpt-5.4-pro":       400_000,
}

// defaultContextWindow is the fallback when a model id isn't recognised.
// Conservative to avoid silently overflowing newly-added models.
const defaultContextWindow = 128_000

// BudgetForModel returns the input-token budget for the given model id,
// falling back to defaultContextWindow when the model isn't in the table.
func BudgetForModel(model string) int {
	if w, ok := contextWindows[model]; ok {
		return w
	}
	return defaultContextWindow
}

// estimateTokens approximates a message's token cost. Uses chars/4 with a
// small per-call overhead — coarse but fine for "is this conversation about
// to overflow" decisions; we don't ship a real tokenizer to keep the
// zero-CGo property and avoid a multi-MB BPE table dependency.
func estimateTokens(m *schema.Message) int {
	if m == nil {
		return 0
	}
	n := (len(m.Content) + len(m.ReasoningContent)) / 4
	for _, tc := range m.ToolCalls {
		n += (len(tc.Function.Name) + len(tc.Function.Arguments)) / 4
		n += 8
	}
	return n + 4
}

func estimateTotal(msgs []*schema.Message) int {
	total := 0
	for _, m := range msgs {
		total += estimateTokens(m)
	}
	return total
}

// outputReserveRatio reserves part of the budget for the model's response.
// 0.25 leaves 25% of the window free.
const outputReserveRatio = 0.25

// RewriteForBudget trims `msgs` so the estimated input-token count stays
// under `budget * (1 - outputReserveRatio)`. Always preserves the system
// message at index 0 (when present) and the final turn (so the user's
// most recent ask is never dropped). Drops oldest turns first, where a
// "turn" is a User message OR an Assistant message together with any
// subsequent Tool messages — dropping a tool result orphaned from its
// assistant tool_call would error out at the provider, so they must move
// in lockstep.
//
// If even keeping only the system + final turn exceeds budget, returns
// that minimal slice — the caller's request will likely fail at the
// provider, but trimming further would discard the user's ask. That's a
// "single message too large" scenario the caller has to handle separately.
func RewriteForBudget(msgs []*schema.Message, budget int) []*schema.Message {
	if budget <= 0 || len(msgs) == 0 {
		return msgs
	}
	threshold := int(float64(budget) * (1 - outputReserveRatio))
	if estimateTotal(msgs) <= threshold {
		return msgs
	}

	// Identify the system prefix and group the rest into trimmable turns.
	start := 0
	if msgs[0] != nil && msgs[0].Role == schema.System {
		start = 1
	}
	groups := groupTurns(msgs[start:])
	if len(groups) <= 1 {
		return msgs
	}

	// Keep dropping the oldest non-final group until we fit or only the
	// final turn remains.
	for len(groups) > 1 {
		trimmed := append([]*schema.Message(nil), msgs[:start]...)
		for _, g := range groups[1:] {
			trimmed = append(trimmed, g...)
		}
		if estimateTotal(trimmed) <= threshold {
			return trimmed
		}
		groups = groups[1:]
	}

	// Only the final turn (+ system) left; return what we have.
	final := append([]*schema.Message(nil), msgs[:start]...)
	if len(groups) == 1 {
		final = append(final, groups[0]...)
	}
	return final
}

// groupTurns splits a non-system message slice into "turns" — adjacent
// runs that move atomically when trimmed. A turn boundary opens on a
// User or Assistant role; subsequent Tool messages join the prior
// Assistant turn so an orphaned tool_result never appears post-trim.
func groupTurns(msgs []*schema.Message) [][]*schema.Message {
	var groups [][]*schema.Message
	for _, m := range msgs {
		if m == nil {
			continue
		}
		switch m.Role {
		case schema.User, schema.Assistant, schema.System:
			groups = append(groups, []*schema.Message{m})
		case schema.Tool:
			if len(groups) == 0 {
				// Orphan tool message at the head — keep it grouped
				// alone; trimming would be incorrect either way.
				groups = append(groups, []*schema.Message{m})
			} else {
				groups[len(groups)-1] = append(groups[len(groups)-1], m)
			}
		default:
			groups = append(groups, []*schema.Message{m})
		}
	}
	return groups
}

// RewriteMargoForBudget is the margo.Message variant of RewriteForBudget,
// used by the non-agent Chat / StreamChat paths in app.go where messages
// haven't been converted into schema.Message yet. The trim algorithm is
// identical: preserve system context, drop oldest turns first, keep tool
// results glued to their assistant tool_calls. Note that margo's request
// model carries the system prompt in `Request.System` rather than as a
// `RoleSystem` message in the slice, so the system slot is implicit and
// every entry in `msgs` is a candidate for trimming except the last turn.
func RewriteMargoForBudget(msgs []margo.Message, systemPrompt string, budget int) []margo.Message {
	if budget <= 0 || len(msgs) == 0 {
		return msgs
	}
	threshold := int(float64(budget) * (1 - outputReserveRatio))
	systemCost := len(systemPrompt) / 4
	if estimateTotalMargo(msgs)+systemCost <= threshold {
		return msgs
	}

	groups := groupTurnsMargo(msgs)
	if len(groups) <= 1 {
		return msgs
	}

	for len(groups) > 1 {
		var trimmed []margo.Message
		for _, g := range groups[1:] {
			trimmed = append(trimmed, g...)
		}
		if estimateTotalMargo(trimmed)+systemCost <= threshold {
			return trimmed
		}
		groups = groups[1:]
	}

	if len(groups) == 1 {
		return groups[0]
	}
	return nil
}

func estimateMargoTokens(m margo.Message) int {
	n := len(m.Content) / 4
	for _, tc := range m.ToolCalls {
		n += (len(tc.Name) + len(tc.Arguments)) / 4
		n += 8
	}
	return n + 4
}

func estimateTotalMargo(msgs []margo.Message) int {
	total := 0
	for _, m := range msgs {
		total += estimateMargoTokens(m)
	}
	return total
}

func groupTurnsMargo(msgs []margo.Message) [][]margo.Message {
	var groups [][]margo.Message
	for _, m := range msgs {
		switch m.Role {
		case margo.RoleUser, margo.RoleAssistant, margo.RoleSystem:
			groups = append(groups, []margo.Message{m})
		case margo.RoleTool:
			if len(groups) == 0 {
				groups = append(groups, []margo.Message{m})
			} else {
				groups[len(groups)-1] = append(groups[len(groups)-1], m)
			}
		default:
			groups = append(groups, []margo.Message{m})
		}
	}
	return groups
}

// budgetRewriter returns a react.AgentConfig.MessageRewriter closure that
// trims accumulated state.Messages to the model's budget on each iteration
// of the ReAct loop. Bound to a model id at construction so we don't have
// to re-resolve the budget per call.
func budgetRewriter(model string) func(context.Context, []*schema.Message) []*schema.Message {
	if strings.TrimSpace(model) == "" {
		return nil
	}
	budget := BudgetForModel(model)
	return func(_ context.Context, msgs []*schema.Message) []*schema.Message {
		return RewriteForBudget(msgs, budget)
	}
}
