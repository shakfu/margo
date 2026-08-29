// Package openaicompat implements margo.Client against the OpenAI Chat
// Completions wire format. Any provider speaking that format — OpenAI
// itself, OpenRouter, and the various self-hosted gateways that emulate
// it — differs only in base URL, default model, and identity headers,
// so those are the only things Options carries.
//
// The package exists because the OpenRouter provider was previously a
// verbatim copy of the OpenAI one. Two copies of an SSE decoder and a
// tool-call fragment reassembler is two places for the same bug, and
// only one of them had test coverage.
//
// Providers wrap this rather than re-export it, so `openai.New` and
// `openrouter.New` keep their own package identity and their own
// provider-specific tests.
package openaicompat

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"

	"github.com/shakfu/margo/pkg/margo"
)

// Options configures a Client.
type Options struct {
	// Name is what margo.Client.Name reports; it is the provider key
	// used throughout core (`"openai"`, `"openrouter"`).
	Name string

	// APIKey is the bearer credential.
	APIKey string

	// BaseURL overrides the SDK default. Empty means OpenAI's own
	// endpoint. Tests set this to an httptest server.
	BaseURL string

	// DefaultModel is used when a Request leaves Model empty.
	DefaultModel string

	// Headers are sent on every request. OpenRouter uses HTTP-Referer
	// and X-Title for app attribution; OpenAI needs none.
	Headers map[string]string

	// ModelFilter, when set, decides which ids from ListModels reach
	// the caller. OpenAI's /v1/models mixes chat models in with
	// embeddings, speech, and image endpoints, none of which belong in
	// a model picker. Nil keeps everything.
	ModelFilter func(id string) bool
}

// Client is a margo.Client speaking the OpenAI Chat Completions format.
type Client struct {
	sdk          sdk.Client
	name         string
	defaultModel string
	modelFilter  func(id string) bool
}

// New builds a Client from opts.
func New(opts Options) *Client {
	sdkOpts := []option.RequestOption{option.WithAPIKey(opts.APIKey)}
	if opts.BaseURL != "" {
		sdkOpts = append(sdkOpts, option.WithBaseURL(opts.BaseURL))
	}
	// Sorted so the option order is deterministic; the SDK applies
	// them in sequence and a stable order keeps test assertions stable.
	for _, k := range sortedKeys(opts.Headers) {
		sdkOpts = append(sdkOpts, option.WithHeader(k, opts.Headers[k]))
	}
	return &Client{
		sdk:          sdk.NewClient(sdkOpts...),
		name:         opts.Name,
		defaultModel: opts.DefaultModel,
		modelFilter:  opts.ModelFilter,
	}
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

func (c *Client) Name() string { return c.name }

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

// toSDKMessage converts a margo.Message into an OpenAI message-param union,
// handling assistant messages with tool calls and tool-result messages.
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
		if len(m.Parts) > 0 {
			return sdk.UserMessage(toSDKUserParts(m))
		}
		return sdk.UserMessage(m.Content)
	}
}

// toSDKUserParts builds an OpenAI multipart user-message content array
// from a margo message. Image parts ride as data: URLs (base64-encoded);
// text parts ride as text content parts. Empty entries are skipped.
func toSDKUserParts(m margo.Message) []sdk.ChatCompletionContentPartUnionParam {
	parts := make([]sdk.ChatCompletionContentPartUnionParam, 0, len(m.Parts)+1)
	hasText := false
	for _, p := range m.Parts {
		switch p.Kind {
		case margo.PartText:
			if p.Text == "" {
				continue
			}
			parts = append(parts, sdk.TextContentPart(p.Text))
			hasText = true
		case margo.PartImage:
			if len(p.Data) == 0 || p.MimeType == "" {
				continue
			}
			dataURL := "data:" + p.MimeType + ";base64," + base64.StdEncoding.EncodeToString(p.Data)
			parts = append(parts, sdk.ImageContentPart(sdk.ChatCompletionContentPartImageImageURLParam{
				URL: dataURL,
			}))
		case margo.PartDocument:
			// OpenAI's chat-completions API has no native document block,
			// so extract text on the Go side (§7.5). Failures fall back
			// to a clear marker so the model can at least mention that
			// the attachment was unreadable, rather than the call
			// silently dropping the part.
			text, err := margo.ExtractTextFromDocument(p, p.Name)
			if err != nil {
				text = fmt.Sprintf("<file name=%q>\n[could not extract: %s]\n</file>", p.Name, err.Error())
			}
			parts = append(parts, sdk.TextContentPart(text))
			hasText = true
		}
	}
	// Preserve the legacy Content string when Parts didn't include any text.
	if !hasText && m.Content != "" {
		parts = append(parts, sdk.TextContentPart(m.Content))
	}
	return parts
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

// pendingToolCall accumulates streamed tool-call deltas keyed by index.
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

		// Emit fully-assembled tool calls in index order before the usage chunk.
		if len(pending) > 0 {
			indices := make([]int64, 0, len(pending))
			for i := range pending {
				indices = append(indices, i)
			}
			// Simple in-place sort; tool-call counts are tiny.
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

// ListModels enumerates the models the endpoint advertises.
//
// The OpenAI /v1/models response carries identifiers and nothing else —
// no context window, no modality, no price — so every returned Model has
// those fields zero and the caller is expected to fill them from the
// embedded catalog. The endpoint also lists non-chat models (embeddings,
// audio, image), which is what Options.ModelFilter is for.
func (c *Client) ListModels(ctx context.Context) ([]margo.Model, error) {
	out := []margo.Model{}
	pager := c.sdk.Models.ListAutoPaging(ctx)
	for pager.Next() {
		id := pager.Current().ID
		if c.modelFilter != nil && !c.modelFilter(id) {
			continue
		}
		out = append(out, margo.Model{ID: id})
	}
	if err := pager.Err(); err != nil {
		return nil, fmt.Errorf("%s: list models: %w", c.name, err)
	}
	return out, nil
}
