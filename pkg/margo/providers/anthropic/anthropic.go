package anthropic

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/shakfu/margo/pkg/margo"
)

type Client struct {
	sdk          sdk.Client
	defaultModel string
}

func New(apiKey string) *Client {
	return &Client{
		sdk:          sdk.NewClient(option.WithAPIKey(apiKey)),
		defaultModel: "claude-haiku-4-5",
	}
}

func (c *Client) Name() string { return "anthropic" }

func (c *Client) buildParams(req margo.Request) sdk.MessageNewParams {
	model := req.Model
	if model == "" {
		model = c.defaultModel
	}
	maxTokens := int64(req.MaxTokens)
	if maxTokens == 0 {
		maxTokens = 1024
	}

	params := sdk.MessageNewParams{
		MaxTokens: maxTokens,
		Model:     sdk.Model(model),
		Messages:  toAnthropicMessages(req.Messages),
	}
	if req.System != "" {
		params.System = []sdk.TextBlockParam{{Text: req.System}}
	}
	if req.Temperature != nil {
		params.Temperature = param.NewOpt(*req.Temperature)
	}
	if req.TopP != nil {
		params.TopP = param.NewOpt(*req.TopP)
	}
	if len(req.StopSequences) > 0 {
		params.StopSequences = req.StopSequences
	}
	if req.Thinking != nil && req.Thinking.Enabled {
		budget := int64(req.Thinking.BudgetTokens)
		if budget < 1024 {
			budget = 1024
		}
		params.Thinking = sdk.ThinkingConfigParamOfEnabled(budget)
	}
	if len(req.Tools) > 0 {
		tools := make([]sdk.ToolUnionParam, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, toAnthropicTool(t))
		}
		params.Tools = tools
	}
	if tc, ok := toolChoice(req.ToolChoice); ok {
		params.ToolChoice = tc
	}
	return params
}

// toAnthropicMessages converts margo messages into Anthropic message params.
//
// Anthropic does not have a "tool" role: tool results are sent as one or more
// tool_result content blocks inside a user message. Consecutive RoleTool
// entries are batched into a single user message so the API accepts them.
func toAnthropicMessages(msgs []margo.Message) []sdk.MessageParam {
	out := make([]sdk.MessageParam, 0, len(msgs))
	i := 0
	for i < len(msgs) {
		m := msgs[i]
		switch m.Role {
		case margo.RoleAssistant:
			blocks := make([]sdk.ContentBlockParamUnion, 0, 1+len(m.ToolCalls))
			if m.Content != "" {
				blocks = append(blocks, sdk.NewTextBlock(m.Content))
			}
			for _, tc := range m.ToolCalls {
				var input any
				if tc.Arguments != "" {
					if err := json.Unmarshal([]byte(tc.Arguments), &input); err != nil {
						input = tc.Arguments
					}
				}
				blocks = append(blocks, sdk.NewToolUseBlock(tc.ID, input, tc.Name))
			}
			out = append(out, sdk.NewAssistantMessage(blocks...))
			i++
		case margo.RoleTool:
			blocks := []sdk.ContentBlockParamUnion{}
			for i < len(msgs) && msgs[i].Role == margo.RoleTool {
				blocks = append(blocks, sdk.NewToolResultBlock(msgs[i].ToolCallID, msgs[i].Content, false))
				i++
			}
			out = append(out, sdk.NewUserMessage(blocks...))
		case margo.RoleSystem:
			// System content belongs in MessageNewParams.System, not here.
			i++
		default:
			out = append(out, sdk.NewUserMessage(sdk.NewTextBlock(m.Content)))
			i++
		}
	}
	return out
}

func toAnthropicTool(t margo.ToolDef) sdk.ToolUnionParam {
	inp := sdk.ToolInputSchemaParam{}
	if t.Parameters != nil {
		if props, ok := t.Parameters["properties"]; ok {
			inp.Properties = props
		}
		if reqRaw, ok := t.Parameters["required"]; ok {
			switch v := reqRaw.(type) {
			case []string:
				inp.Required = v
			case []any:
				for _, r := range v {
					if s, ok := r.(string); ok {
						inp.Required = append(inp.Required, s)
					}
				}
			}
		}
	}
	tp := sdk.ToolParam{
		Name:        t.Name,
		InputSchema: inp,
	}
	if t.Description != "" {
		tp.Description = param.NewOpt(t.Description)
	}
	return sdk.ToolUnionParam{OfTool: &tp}
}

func toolChoice(s string) (sdk.ToolChoiceUnionParam, bool) {
	switch s {
	case "":
		return sdk.ToolChoiceUnionParam{}, false
	case "auto":
		return sdk.ToolChoiceUnionParam{OfAuto: &sdk.ToolChoiceAutoParam{}}, true
	case "required":
		// Anthropic's "any" forces the model to call some tool.
		return sdk.ToolChoiceUnionParam{OfAny: &sdk.ToolChoiceAnyParam{}}, true
	case "none":
		none := sdk.NewToolChoiceNoneParam()
		return sdk.ToolChoiceUnionParam{OfNone: &none}, true
	default:
		return sdk.ToolChoiceUnionParam{OfTool: &sdk.ToolChoiceToolParam{Name: s}}, true
	}
}

func (c *Client) Complete(ctx context.Context, req margo.Request) (margo.Response, error) {
	msg, err := c.sdk.Messages.New(ctx, c.buildParams(req))
	if err != nil {
		return margo.Response{}, err
	}
	var text, thinking strings.Builder
	var toolCalls []margo.ToolCall
	for _, block := range msg.Content {
		switch v := block.AsAny().(type) {
		case sdk.TextBlock:
			text.WriteString(v.Text)
		case sdk.ThinkingBlock:
			thinking.WriteString(v.Thinking)
		case sdk.ToolUseBlock:
			toolCalls = append(toolCalls, margo.ToolCall{
				ID:        v.ID,
				Name:      v.Name,
				Arguments: string(v.Input),
			})
		}
	}
	return margo.Response{
		Text:      text.String(),
		Thinking:  thinking.String(),
		Model:     string(msg.Model),
		ToolCalls: toolCalls,
		Usage: margo.Usage{
			InputTokens:  int(msg.Usage.InputTokens),
			OutputTokens: int(msg.Usage.OutputTokens),
		},
	}, nil
}

// pendingToolUse accumulates streamed tool_use deltas keyed by content-block index.
type pendingToolUse struct {
	id, name string
	input    strings.Builder
}

func (c *Client) Stream(ctx context.Context, req margo.Request) (<-chan margo.Chunk, error) {
	stream := c.sdk.Messages.NewStreaming(ctx, c.buildParams(req))
	out := make(chan margo.Chunk, 16)
	go func() {
		defer close(out)
		defer stream.Close()

		started := time.Now()
		var firstToken time.Time
		usage := margo.Usage{}
		pending := map[int64]*pendingToolUse{}

		for stream.Next() {
			ev := stream.Current()
			switch ev.Type {
			case "message_start":
				ms := ev.AsMessageStart()
				usage.InputTokens = int(ms.Message.Usage.InputTokens)
			case "content_block_start":
				start := ev.AsContentBlockStart()
				if start.ContentBlock.Type == "tool_use" {
					pending[start.Index] = &pendingToolUse{
						id:   start.ContentBlock.ID,
						name: start.ContentBlock.Name,
					}
				}
			case "content_block_delta":
				cbd := ev.AsContentBlockDelta()
				delta := cbd.Delta
				switch delta.Type {
				case "text_delta":
					if firstToken.IsZero() {
						firstToken = time.Now()
					}
					if delta.Text != "" {
						select {
						case out <- margo.Chunk{Kind: margo.ChunkText, Text: delta.Text}:
						case <-ctx.Done():
							return
						}
					}
				case "thinking_delta":
					if firstToken.IsZero() {
						firstToken = time.Now()
					}
					if delta.Thinking != "" {
						select {
						case out <- margo.Chunk{Kind: margo.ChunkThinking, Text: delta.Thinking}:
						case <-ctx.Done():
							return
						}
					}
				case "input_json_delta":
					if p, ok := pending[cbd.Index]; ok && delta.PartialJSON != "" {
						p.input.WriteString(delta.PartialJSON)
					}
				}
			case "content_block_stop":
				stop := ev.AsContentBlockStop()
				if p, ok := pending[stop.Index]; ok {
					delete(pending, stop.Index)
					args := p.input.String()
					if args == "" {
						args = "{}"
					}
					tc := margo.ToolCall{ID: p.id, Name: p.name, Arguments: args}
					select {
					case out <- margo.Chunk{Kind: margo.ChunkToolCall, ToolCall: &tc}:
					case <-ctx.Done():
						return
					}
				}
			case "message_delta":
				md := ev.AsMessageDelta()
				usage.OutputTokens = int(md.Usage.OutputTokens)
			}
		}
		if err := stream.Err(); err != nil {
			out <- margo.Chunk{Err: err}
			return
		}

		now := time.Now()
		usage.TotalMs = now.Sub(started).Milliseconds()
		if !firstToken.IsZero() {
			usage.FirstTokenMs = firstToken.Sub(started).Milliseconds()
		}
		u := usage
		out <- margo.Chunk{Usage: &u}
	}()
	return out, nil
}
