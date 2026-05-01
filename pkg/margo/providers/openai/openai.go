package openai

import (
	"context"
	"strings"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/shakfu/margo/pkg/margo"
)

type Client struct {
	sdk          sdk.Client
	defaultModel string
}

func New(apiKey string) *Client {
	return &Client{
		sdk:          sdk.NewClient(option.WithAPIKey(apiKey)),
		defaultModel: sdk.ChatModelGPT5_2,
	}
}

func (c *Client) Name() string { return "openai" }

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
		switch m.Role {
		case margo.RoleAssistant:
			msgs = append(msgs, sdk.AssistantMessage(m.Content))
		default:
			msgs = append(msgs, sdk.UserMessage(m.Content))
		}
	}

	params := sdk.ChatCompletionNewParams{
		Model:    sdk.ChatModel(model),
		Messages: msgs,
	}
	if req.MaxTokens > 0 {
		params.MaxCompletionTokens = sdk.Int(int64(req.MaxTokens))
	}
	return params
}

func (c *Client) Complete(ctx context.Context, req margo.Request) (margo.Response, error) {
	resp, err := c.sdk.Chat.Completions.New(ctx, c.buildParams(req))
	if err != nil {
		return margo.Response{}, err
	}
	var b strings.Builder
	for _, ch := range resp.Choices {
		b.WriteString(ch.Message.Content)
	}
	return margo.Response{Text: b.String(), Model: string(resp.Model)}, nil
}

func (c *Client) Stream(ctx context.Context, req margo.Request) (<-chan margo.Chunk, error) {
	stream := c.sdk.Chat.Completions.NewStreaming(ctx, c.buildParams(req))
	out := make(chan margo.Chunk, 16)
	go func() {
		defer close(out)
		defer stream.Close()
		for stream.Next() {
			chunk := stream.Current()
			for _, choice := range chunk.Choices {
				if choice.Delta.Content != "" {
					select {
					case out <- margo.Chunk{Text: choice.Delta.Content}:
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
