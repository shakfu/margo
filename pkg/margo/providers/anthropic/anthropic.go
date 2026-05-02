package anthropic

import (
	"context"
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

	msgs := make([]sdk.MessageParam, 0, len(req.Messages))
	for _, m := range req.Messages {
		block := sdk.NewTextBlock(m.Content)
		switch m.Role {
		case margo.RoleAssistant:
			msgs = append(msgs, sdk.NewAssistantMessage(block))
		default:
			msgs = append(msgs, sdk.NewUserMessage(block))
		}
	}

	params := sdk.MessageNewParams{
		MaxTokens: maxTokens,
		Model:     sdk.Model(model),
		Messages:  msgs,
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
	return params
}

func (c *Client) Complete(ctx context.Context, req margo.Request) (margo.Response, error) {
	msg, err := c.sdk.Messages.New(ctx, c.buildParams(req))
	if err != nil {
		return margo.Response{}, err
	}
	var text, thinking strings.Builder
	for _, block := range msg.Content {
		switch v := block.AsAny().(type) {
		case sdk.TextBlock:
			text.WriteString(v.Text)
		case sdk.ThinkingBlock:
			thinking.WriteString(v.Thinking)
		}
	}
	return margo.Response{
		Text:     text.String(),
		Thinking: thinking.String(),
		Model:    string(msg.Model),
		Usage: margo.Usage{
			InputTokens:  int(msg.Usage.InputTokens),
			OutputTokens: int(msg.Usage.OutputTokens),
		},
	}, nil
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

		for stream.Next() {
			ev := stream.Current()
			switch ev.Type {
			case "message_start":
				ms := ev.AsMessageStart()
				usage.InputTokens = int(ms.Message.Usage.InputTokens)
			case "content_block_delta":
				delta := ev.AsContentBlockDelta().Delta
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
