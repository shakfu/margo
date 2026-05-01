package anthropic

import (
	"context"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/shakfu/margo/pkg/margo"
)

type Client struct {
	sdk          sdk.Client
	defaultModel string
}

func New(apiKey string) *Client {
	return &Client{
		sdk:          sdk.NewClient(option.WithAPIKey(apiKey)),
		defaultModel: sdk.ModelClaudeOpus4_6,
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
	return params
}

func (c *Client) Complete(ctx context.Context, req margo.Request) (margo.Response, error) {
	msg, err := c.sdk.Messages.New(ctx, c.buildParams(req))
	if err != nil {
		return margo.Response{}, err
	}
	var b strings.Builder
	for _, block := range msg.Content {
		b.WriteString(block.Text)
	}
	return margo.Response{Text: b.String(), Model: string(msg.Model)}, nil
}

func (c *Client) Stream(ctx context.Context, req margo.Request) (<-chan margo.Chunk, error) {
	stream := c.sdk.Messages.NewStreaming(ctx, c.buildParams(req))
	out := make(chan margo.Chunk, 16)
	go func() {
		defer close(out)
		defer stream.Close()
		for stream.Next() {
			ev := stream.Current()
			if ev.Type == "content_block_delta" {
				delta := ev.AsContentBlockDelta().Delta.AsTextDelta()
				if delta.Text != "" {
					select {
					case out <- margo.Chunk{Text: delta.Text}:
					case <-ctx.Done():
						return
					}
				}
			}
		}
		if err := stream.Err(); err != nil {
			out <- margo.Chunk{Err: err}
		}
	}()
	return out, nil
}
