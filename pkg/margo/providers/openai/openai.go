package openai

import (
	"context"
	"strings"
	"time"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"

	"github.com/shakfu/margo/pkg/margo"
)

type Client struct {
	sdk          sdk.Client
	defaultModel string
}

func New(apiKey string) *Client {
	return &Client{
		sdk:          sdk.NewClient(option.WithAPIKey(apiKey)),
		defaultModel: "gpt-5.4-nano",
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
	if req.Temperature != nil {
		params.Temperature = param.NewOpt(*req.Temperature)
	}
	if req.TopP != nil {
		params.TopP = param.NewOpt(*req.TopP)
	}
	if len(req.StopSequences) > 0 {
		params.Stop = sdk.ChatCompletionNewParamsStopUnion{OfStringArray: req.StopSequences}
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
	return margo.Response{
		Text:  b.String(),
		Model: string(resp.Model),
		Usage: margo.Usage{
			InputTokens:  int(resp.Usage.PromptTokens),
			OutputTokens: int(resp.Usage.CompletionTokens),
		},
	}, nil
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
