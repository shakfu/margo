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

func (c *Client) Complete(ctx context.Context, req margo.Request) (margo.Response, error) {
	model := req.Model
	if model == "" {
		model = c.defaultModel
	}
	maxTokens := int64(req.MaxTokens)
	if maxTokens == 0 {
		maxTokens = 1024
	}

	msg, err := c.sdk.Messages.New(ctx, sdk.MessageNewParams{
		MaxTokens: maxTokens,
		Model:     sdk.Model(model),
		Messages: []sdk.MessageParam{
			sdk.NewUserMessage(sdk.NewTextBlock(req.Prompt)),
		},
	})
	if err != nil {
		return margo.Response{}, err
	}

	var b strings.Builder
	for _, block := range msg.Content {
		b.WriteString(block.Text)
	}
	return margo.Response{Text: b.String(), Model: string(msg.Model)}, nil
}
