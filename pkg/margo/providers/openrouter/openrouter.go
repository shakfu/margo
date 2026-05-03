package openrouter

import (
	"context"
	"strings"
	"time"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"

	"github.com/shakfu/margo/pkg/margo"
)

const baseURL = "https://openrouter.ai/api/v1"

type Client struct {
	sdk          sdk.Client
	defaultModel string
}

func New(apiKey string) *Client {
	return &Client{
		sdk: sdk.NewClient(
			option.WithAPIKey(apiKey),
			option.WithBaseURL(baseURL),
			option.WithHeader("HTTP-Referer", "https://github.com/shakfu/margo"),
			option.WithHeader("X-Title", "margo"),
		),
		defaultModel: "deepseek/deepseek-v3.2",
	}
}

func (c *Client) Name() string { return "openrouter" }

func (c *Client) buildParams(req margo.Request) sdk.ChatCompletionNewParams {
	model := req.Model
	if model == "" {
		model = c.defaultModel
	}

	msgs := make([]sdk.ChatCompletionMessageParamUnion, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, sdk.SystemMessage(req.System))
	}
	for _, m := range req.Messages {
		msgs = append(msgs, toSDKMessage(m))
	}

	params := sdk.ChatCompletionNewParams{
		Model:    sdk.ChatModel(model),
		Messages: msgs,
	}
	if req.MaxTokens > 0 {
		params.MaxCompletionTokens = sdk.Int(int64(req.MaxTokens))
	}
	if req.Temperature != nil {
		params.Temperature = param.NewOpt(*req.Temperature)
	}
	if req.TopP != nil {
		params.TopP = param.NewOpt(*req.TopP)
	}
	if len(req.StopSequences) > 0 {
		params.Stop = sdk.ChatCompletionNewParamsStopUnion{OfStringArray: req.StopSequences}
	}
	if len(req.Tools) > 0 {
		params.Tools = toSDKTools(req.Tools)
	}
	if tc, ok := toolChoice(req.ToolChoice); ok {
		params.ToolChoice = tc
	}
	return params
}

func toSDKMessage(m margo.Message) sdk.ChatCompletionMessageParamUnion {
	switch m.Role {
	case margo.RoleAssistant:
		if len(m.ToolCalls) == 0 {
			return sdk.AssistantMessage(m.Content)
		}
		assistant := sdk.ChatCompletionAssistantMessageParam{}
		if m.Content != "" {
			assistant.Content = sdk.ChatCompletionAssistantMessageParamContentUnion{
				OfString: param.NewOpt(m.Content),
			}
		}
		assistant.ToolCalls = make([]sdk.ChatCompletionMessageToolCallUnionParam, 0, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			assistant.ToolCalls = append(assistant.ToolCalls, sdk.ChatCompletionMessageToolCallUnionParam{
				OfFunction: &sdk.ChatCompletionMessageFunctionToolCallParam{
					ID: tc.ID,
					Function: sdk.ChatCompletionMessageFunctionToolCallFunctionParam{
						Name:      tc.Name,
						Arguments: tc.Arguments,
					},
				},
			})
		}
		return sdk.ChatCompletionMessageParamUnion{OfAssistant: &assistant}
	case margo.RoleTool:
		return sdk.ToolMessage(m.Content, m.ToolCallID)
	case margo.RoleSystem:
		return sdk.SystemMessage(m.Content)
	default:
		return sdk.UserMessage(m.Content)
	}
}

func toSDKTools(tools []margo.ToolDef) []sdk.ChatCompletionToolUnionParam {
	out := make([]sdk.ChatCompletionToolUnionParam, 0, len(tools))
	for _, t := range tools {
		fn := shared.FunctionDefinitionParam{Name: t.Name}
		if t.Description != "" {
			fn.Description = param.NewOpt(t.Description)
		}
		if t.Parameters != nil {
			fn.Parameters = shared.FunctionParameters(t.Parameters)
		}
		out = append(out, sdk.ChatCompletionFunctionTool(fn))
	}
	return out
}

func toolChoice(s string) (sdk.ChatCompletionToolChoiceOptionUnionParam, bool) {
	switch s {
	case "":
		return sdk.ChatCompletionToolChoiceOptionUnionParam{}, false
	case "auto", "none", "required":
		return sdk.ChatCompletionToolChoiceOptionUnionParam{OfAuto: param.NewOpt(s)}, true
	default:
		return sdk.ChatCompletionToolChoiceOptionUnionParam{
			OfFunctionToolChoice: &sdk.ChatCompletionNamedToolChoiceParam{
				Type:     "function",
				Function: sdk.ChatCompletionNamedToolChoiceFunctionParam{Name: s},
			},
		}, true
	}
}

func (c *Client) Complete(ctx context.Context, req margo.Request) (margo.Response, error) {
	resp, err := c.sdk.Chat.Completions.New(ctx, c.buildParams(req))
	if err != nil {
		return margo.Response{}, err
	}
	var b strings.Builder
	var toolCalls []margo.ToolCall
	for _, ch := range resp.Choices {
		b.WriteString(ch.Message.Content)
		for _, tc := range ch.Message.ToolCalls {
			fn := tc.AsFunction()
			toolCalls = append(toolCalls, margo.ToolCall{
				ID:        fn.ID,
				Name:      fn.Function.Name,
				Arguments: fn.Function.Arguments,
			})
		}
	}
	return margo.Response{
		Text:      b.String(),
		Model:     string(resp.Model),
		ToolCalls: toolCalls,
		Usage: margo.Usage{
			InputTokens:  int(resp.Usage.PromptTokens),
			OutputTokens: int(resp.Usage.CompletionTokens),
		},
	}, nil
}

type pendingToolCall struct {
	id, name string
	args     strings.Builder
}

func (c *Client) Stream(ctx context.Context, req margo.Request) (<-chan margo.Chunk, error) {
	params := c.buildParams(req)
	params.StreamOptions = sdk.ChatCompletionStreamOptionsParam{
		IncludeUsage: param.NewOpt(true),
	}
	stream := c.sdk.Chat.Completions.NewStreaming(ctx, params)
	out := make(chan margo.Chunk, 16)
	go func() {
		defer close(out)
		defer stream.Close()

		started := time.Now()
		var firstToken time.Time
		usage := margo.Usage{}
		pending := map[int64]*pendingToolCall{}

		for stream.Next() {
			chunk := stream.Current()
			if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
				usage.InputTokens = int(chunk.Usage.PromptTokens)
				usage.OutputTokens = int(chunk.Usage.CompletionTokens)
			}
			for _, choice := range chunk.Choices {
				if choice.Delta.Content != "" {
					if firstToken.IsZero() {
						firstToken = time.Now()
					}
					select {
					case out <- margo.Chunk{Kind: margo.ChunkText, Text: choice.Delta.Content}:
					case <-ctx.Done():
						return
					}
				}
				for _, tc := range choice.Delta.ToolCalls {
					p, ok := pending[tc.Index]
					if !ok {
						p = &pendingToolCall{}
						pending[tc.Index] = p
					}
					if tc.ID != "" {
						p.id = tc.ID
					}
					if tc.Function.Name != "" {
						p.name = tc.Function.Name
					}
					if tc.Function.Arguments != "" {
						p.args.WriteString(tc.Function.Arguments)
					}
				}
			}
		}
		if err := stream.Err(); err != nil {
			out <- margo.Chunk{Err: err}
			return
		}

		if len(pending) > 0 {
			indices := make([]int64, 0, len(pending))
			for i := range pending {
				indices = append(indices, i)
			}
			for i := 1; i < len(indices); i++ {
				for j := i; j > 0 && indices[j-1] > indices[j]; j-- {
					indices[j-1], indices[j] = indices[j], indices[j-1]
				}
			}
			for _, i := range indices {
				p := pending[i]
				tc := margo.ToolCall{ID: p.id, Name: p.name, Arguments: p.args.String()}
				select {
				case out <- margo.Chunk{Kind: margo.ChunkToolCall, ToolCall: &tc}:
				case <-ctx.Done():
					return
				}
			}
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
