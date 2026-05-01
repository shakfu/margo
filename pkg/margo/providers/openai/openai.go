package openai

import (
	"context"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"

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

func (c *Client) Complete(ctx context.Context, req margo.Request) (margo.Response, error) {
	model := req.Model
	if model == "" {
		model = c.defaultModel
	}

	resp, err := c.sdk.Responses.New(ctx, responses.ResponseNewParams{
		Input: responses.ResponseNewParamsInputUnion{OfString: sdk.String(req.Prompt)},
		Model: responses.ResponsesModel(model),
	})
	if err != nil {
		return margo.Response{}, err
	}
	return margo.Response{Text: resp.OutputText(), Model: model}, nil
}
