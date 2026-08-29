package agent

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/shakfu/margo/pkg/margo"
)

// WorkflowRunner runs a fixed three-stage sequential pipeline:
// **drafter → critic → refiner**. Each stage is a separate
// `adk.ChatModelAgent` with its own system prompt; the kit's
// `adk.NewSequentialAgent` feeds each stage's output forward as part
// of the running conversation, so the critic sees the drafter's
// output and the refiner sees both.
//
// The three default prompts are domain-neutral (draft → critique →
// polish). When §8.3 lands, this runner will be re-pointed at a
// user-configurable sub-agent chain sourced from the workspace
// settings — the structure here (assemble N ChatModelAgents around a
// shared Adapter, hand them to NewSequentialAgent) won't change.
//
// Registered as RunnerWorkflow ("workflow"); backs the
// `/agent-workflow <task>` slash command. The AgentEvent → StepEvent
// bridge is shared with the React and Plan-Execute runners; tool
// calls inside any stage surface as the same StepToolCall /
// StepToolResult / StepToolStream the UI already renders.
type WorkflowRunner struct{}

const (
	workflowDrafterPrompt = `You are the **drafter** in a three-stage writing pipeline. Produce a thorough initial response to the user's request — focus on substance and completeness rather than polish. The next stages will critique and refine your draft, so leave room for that work: don't apologise for being unfinished, just write directly. If tools are available and relevant, use them to ground the draft in real information.`

	workflowCriticPrompt = `You are the **critic** in a three-stage writing pipeline. The drafter's initial response is in the conversation above. Review it for: clarity, accuracy, completeness, structure, and tone. Output a concise bulleted critique (3-7 points). Do not rewrite the draft. Do not call tools — your job is judgement, not research. If the draft is genuinely strong, say so plainly rather than inventing nitpicks.`

	workflowRefinerPrompt = `You are the **refiner** in a three-stage writing pipeline. The conversation above contains: (1) the user's original request, (2) the drafter's initial response, (3) the critic's review. Produce the **final** polished response that incorporates the critic's feedback. Output only the polished result — no preamble explaining what you changed.`
)

func (WorkflowRunner) Run(
	ctx context.Context,
	c margo.Client,
	defaults margo.Request,
	tools []tool.BaseTool,
	input []*schema.Message,
	attachments []margo.Part,
	gate PermissionGate,
	emit func(StepEvent),
) error {
	run := prepareRun(ctx, c, defaults, attachments, gate, emit)

	stages := []struct {
		name        string
		description string
		instruction string
		tools       []tool.BaseTool
	}{
		{
			name:        "drafter",
			description: "Produces an initial draft response.",
			instruction: workflowDrafterPrompt,
			// Drafter gets the full tool palette — research and
			// retrieval are most useful at the first pass.
			tools: tools,
		},
		{
			name:        "critic",
			description: "Reviews the drafter's output and surfaces a critique.",
			instruction: workflowCriticPrompt,
			// Critic gets no tools — its system prompt forbids
			// tool calls, and removing them from the palette
			// prevents the model from being tempted.
			tools: nil,
		},
		{
			name:        "refiner",
			description: "Produces the final polished response.",
			instruction: workflowRefinerPrompt,
			// Refiner gets no tools — by this stage the draft is
			// in hand and the work is presentation, not research.
			tools: nil,
		},
	}

	subAgents := make([]adk.Agent, 0, len(stages))
	for _, st := range stages {
		a, err := adk.NewChatModelAgent(run.ctx, &adk.ChatModelAgentConfig{
			Name:        st.name,
			Description: st.description,
			Instruction: st.instruction,
			Model:       run.adapter,
			ToolsConfig: run.toolsConfig(st.tools),
		})
		if err != nil {
			return fmt.Errorf("workflow: new %s agent: %w", st.name, err)
		}
		subAgents = append(subAgents, a)
	}

	entry, err := adk.NewSequentialAgent(run.ctx, &adk.SequentialAgentConfig{
		Name:        "margo-workflow",
		Description: "Three-stage drafter -> critic -> refiner writing pipeline.",
		SubAgents:   subAgents,
	})
	if err != nil {
		return fmt.Errorf("workflow: assemble sequential: %w", err)
	}

	return runADKAgent(run.ctx, entry, input, run.emit)
}
