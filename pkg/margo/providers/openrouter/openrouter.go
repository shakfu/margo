// Package openrouter implements margo.Client against OpenRouter using
// its Go SDK (github.com/OpenRouterTeam/go-sdk).
//
// OpenRouter speaks the OpenAI Chat Completions wire format, and this
// provider previously shared providers/openaicompat with OpenAI. It now
// uses the OpenRouter SDK so OpenRouter-only fields are reachable: the
// `reasoning` request option and streamed reasoning deltas, which the
// openai-go types cannot express, and the richer model catalog.
//
// The SDK is beta and pinned to an exact version in go.mod.
package openrouter

import (
	"context"
	"encoding/base64"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	sdk "github.com/OpenRouterTeam/go-sdk"
	"github.com/OpenRouterTeam/go-sdk/models/components"
	"github.com/OpenRouterTeam/go-sdk/models/operations"
	"github.com/OpenRouterTeam/go-sdk/optionalnullable"
	"github.com/OpenRouterTeam/go-sdk/retry"

	"github.com/shakfu/margo/pkg/margo"
)

// baseURL is OpenRouter's API root.
const baseURL = "https://openrouter.ai/api/v1"

// defaultModel is used when a Request leaves Model empty.
const defaultModel = "deepseek/deepseek-v3.2"

// identityHeaders are what OpenRouter attributes traffic with. Without
// them requests are attributed generically and free-tier rate limits
// apply to the pool rather than to margo. Sent per request: the SDK's
// WithHTTPReferer/WithXTitle globals are not applied to chat calls in
// v0.9.21.
var identityHeaders = map[string]string{
	"HTTP-Referer": "https://github.com/shakfu/margo",
	"X-Title":      "margo",
}

// retryPolicy replaces the SDK default, which retries 5xx responses
// for up to an hour. Roughly matches openai-go: a few retries within
// ten seconds.
var retryPolicy = retry.Config{
	Strategy: "backoff",
	Backoff: &retry.BackoffStrategy{
		InitialInterval: 500,
		MaxInterval:     4000,
		Exponent:        2,
		MaxElapsedTime:  10_000,
	},
	RetryConnectionErrors: true,
}

// Client is a margo.Client and margo.ModelLister for OpenRouter.
type Client struct {
	sdk *sdk.OpenRouter
}

// New returns a client for the OpenRouter API.
func New(apiKey string) *Client {
	return newClient(apiKey, baseURL, retryPolicy)
}

func newClient(apiKey, serverURL string, rp retry.Config) *Client {
	return &Client{sdk: sdk.New(
		sdk.WithSecurity(apiKey),
		sdk.WithServerURL(serverURL),
		sdk.WithRetryConfig(rp),
	)}
}

func (c *Client) Name() string { return "openrouter" }

func (c *Client) buildRequest(req margo.Request) components.ChatRequest {
	model := req.Model
	if model == "" {
		model = defaultModel
	}

	msgs := make([]components.ChatMessages, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, systemMessage(req.System))
	}
	for _, m := range req.Messages {
		msgs = append(msgs, toSDKMessage(m))
	}

	out := components.ChatRequest{Model: sdk.String(model), Messages: msgs}
	if req.MaxTokens > 0 {
		out.MaxCompletionTokens = optionalnullable.From(sdk.Int64(int64(req.MaxTokens)))
	}
	if req.Temperature != nil {
		out.Temperature = optionalnullable.From(req.Temperature)
	}
	if req.TopP != nil {
		out.TopP = optionalnullable.From(req.TopP)
	}
	if len(req.StopSequences) > 0 {
		out.Stop = optionalnullable.From(sdk.Pointer(components.CreateStopArrayOfStr(req.StopSequences)))
	}
	if len(req.Tools) > 0 {
		out.Tools = toSDKTools(req.Tools)
	}
	if tc, ok := toolChoice(req.ToolChoice); ok {
		out.ToolChoice = &tc
	}
	if req.Thinking != nil && req.Thinking.Enabled {
		out.Reasoning = &components.ChatRequestReasoning{
			Effort: optionalnullable.From(sdk.Pointer(effortFor(req.Thinking.BudgetTokens))),
		}
	}
	return out
}

// effortFor maps margo's token budget onto OpenRouter's effort levels.
// The SDK's reasoning type has no max_tokens field, so the budget
// cannot be passed through. Thresholds bracket the UI default of 4096.
func effortFor(budget int) components.ChatRequestEffort {
	switch {
	case budget < 4096:
		return components.ChatRequestEffortLow
	case budget < 16384:
		return components.ChatRequestEffortMedium
	default:
		return components.ChatRequestEffortHigh
	}
}

func systemMessage(s string) components.ChatMessages {
	return components.CreateChatMessagesSystem(components.ChatSystemMessage{
		Role:    components.ChatSystemMessageRoleSystem,
		Content: components.CreateChatSystemMessageContentStr(s),
	})
}

// toSDKMessage converts a margo.Message into an OpenRouter message union,
// handling assistant messages with tool calls and tool-result messages.
func toSDKMessage(m margo.Message) components.ChatMessages {
	switch m.Role {
	case margo.RoleAssistant:
		a := components.ChatAssistantMessage{Role: components.ChatAssistantMessageRoleAssistant}
		if m.Content != "" || len(m.ToolCalls) == 0 {
			a.Content = optionalnullable.From(sdk.Pointer(components.CreateChatAssistantMessageContentStr(m.Content)))
		}
		for _, tc := range m.ToolCalls {
			a.ToolCalls = append(a.ToolCalls, components.ChatToolCall{
				ID:       tc.ID,
				Type:     components.ChatToolCallTypeFunction,
				Function: components.ChatToolCallFunction{Name: tc.Name, Arguments: tc.Arguments},
			})
		}
		return components.CreateChatMessagesAssistant(a)
	case margo.RoleTool:
		return components.CreateChatMessagesTool(components.ChatToolMessage{
			Role:       components.ChatToolMessageRoleTool,
			ToolCallID: m.ToolCallID,
			Content:    components.CreateChatToolMessageContentStr(m.Content),
		})
	case margo.RoleSystem:
		return systemMessage(m.Content)
	default:
		content := components.CreateChatUserMessageContentStr(m.Content)
		if len(m.Parts) > 0 {
			content = components.CreateChatUserMessageContentArrayOfChatContentItems(toSDKUserParts(m))
		}
		return components.CreateChatMessagesUser(components.ChatUserMessage{
			Role:    components.ChatUserMessageRoleUser,
			Content: content,
		})
	}
}

func textItem(s string) components.ChatContentItems {
	return components.CreateChatContentItemsText(components.ChatContentText{
		Type: components.ChatContentTextTypeText,
		Text: s,
	})
}

// toSDKUserParts builds a multipart user-message content array. Images
// ride as base64 data: URLs; documents are extracted to text on the Go
// side (§7.5). Empty entries are skipped.
func toSDKUserParts(m margo.Message) []components.ChatContentItems {
	parts := make([]components.ChatContentItems, 0, len(m.Parts)+1)
	hasText := false
	for _, p := range m.Parts {
		switch p.Kind {
		case margo.PartText:
			if p.Text == "" {
				continue
			}
			parts = append(parts, textItem(p.Text))
			hasText = true
		case margo.PartImage:
			if len(p.Data) == 0 || p.MimeType == "" {
				continue
			}
			parts = append(parts, components.CreateChatContentItemsImageURL(components.ChatContentImage{
				Type: components.ChatContentImageTypeImageURL,
				ImageURL: components.ChatContentImageImageURL{
					URL: "data:" + p.MimeType + ";base64," + base64.StdEncoding.EncodeToString(p.Data),
				},
			}))
		case margo.PartDocument:
			// Failures become a marker so the model can say the attachment
			// was unreadable instead of the part vanishing.
			text, err := margo.ExtractTextFromDocument(p, p.Name)
			if err != nil {
				text = fmt.Sprintf("<file name=%q>\n[could not extract: %s]\n</file>", p.Name, err.Error())
			}
			parts = append(parts, textItem(text))
			hasText = true
		}
	}
	// Preserve the Content string when Parts carried no text.
	if !hasText && m.Content != "" {
		parts = append(parts, textItem(m.Content))
	}
	return parts
}

func toSDKTools(tools []margo.ToolDef) []components.ChatFunctionTool {
	out := make([]components.ChatFunctionTool, 0, len(tools))
	for _, t := range tools {
		fn := components.ChatFunctionToolFunctionFunction{Name: t.Name, Parameters: t.Parameters}
		if t.Description != "" {
			fn.Description = sdk.String(t.Description)
		}
		out = append(out, components.CreateChatFunctionToolChatFunctionToolFunction(components.ChatFunctionToolFunction{
			Type:     components.ChatFunctionToolTypeFunction,
			Function: fn,
		}))
	}
	return out
}

func toolChoice(s string) (components.ChatToolChoice, bool) {
	switch s {
	case "":
		return components.ChatToolChoice{}, false
	case "auto":
		return components.CreateChatToolChoiceChatToolChoiceAuto(components.ChatToolChoiceAutoAuto), true
	case "none":
		return components.CreateChatToolChoiceChatToolChoiceNone(components.ChatToolChoiceNoneNone), true
	case "required":
		return components.CreateChatToolChoiceChatToolChoiceRequired(components.ChatToolChoiceRequiredRequired), true
	default:
		return components.CreateChatToolChoiceChatNamedToolChoice(components.ChatNamedToolChoice{
			Type:     components.ChatNamedToolChoiceTypeFunction,
			Function: components.ChatNamedToolChoiceFunction{Name: s},
		}), true
	}
}

func (c *Client) Complete(ctx context.Context, req margo.Request) (margo.Response, error) {
	body := c.buildRequest(req)
	body.Stream = sdk.Bool(false)
	res, err := c.sdk.Chat.Send(ctx, body, nil, operations.WithSetHeaders(identityHeaders))
	if err != nil {
		return margo.Response{}, fmt.Errorf("openrouter: %w", err)
	}
	if res.ChatResult == nil {
		return margo.Response{}, fmt.Errorf("openrouter: expected a JSON completion, got %s", res.Type)
	}
	r := res.ChatResult

	var text, thinking strings.Builder
	var toolCalls []margo.ToolCall
	for _, ch := range r.Choices {
		if content, ok := ch.Message.Content.GetOrZero(); ok && content.Str != nil {
			text.WriteString(*content.Str)
		}
		if reasoning, ok := ch.Message.Reasoning.GetOrZero(); ok {
			thinking.WriteString(reasoning)
		}
		for _, tc := range ch.Message.ToolCalls {
			toolCalls = append(toolCalls, margo.ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
		}
	}
	out := margo.Response{Text: text.String(), Thinking: thinking.String(), Model: r.Model, ToolCalls: toolCalls}
	if r.Usage != nil {
		out.Usage = toUsage(r.Usage)
	}
	return out, nil
}

// pendingToolCall accumulates streamed tool-call deltas keyed by index.
type pendingToolCall struct {
	id, name string
	args     strings.Builder
}

func (c *Client) Stream(ctx context.Context, req margo.Request) (<-chan margo.Chunk, error) {
	body := c.buildRequest(req)
	body.Stream = sdk.Bool(true)

	out := make(chan margo.Chunk, 16)
	// Send runs inside the goroutine so HTTP errors arrive as an Err
	// chunk, as they do from the other providers, and Stream does not
	// block until the response headers arrive.
	go func() {
		defer close(out)

		send := func(ch margo.Chunk) bool {
			select {
			case out <- ch:
				return true
			case <-ctx.Done():
				return false
			}
		}

		started := time.Now()
		res, err := c.sdk.Chat.Send(ctx, body, nil,
			operations.WithSetHeaders(identityHeaders),
			operations.WithAcceptHeaderOverride(operations.AcceptHeaderEnumTextEventStream),
		)
		if err != nil {
			send(margo.Chunk{Err: fmt.Errorf("openrouter: %w", err)})
			return
		}
		if res.EventStream == nil {
			send(margo.Chunk{Err: fmt.Errorf("openrouter: expected an event stream, got %s", res.Type)})
			return
		}
		stream := res.EventStream
		defer func() { _ = stream.Close() }()

		var firstToken time.Time
		usage := margo.Usage{}
		pending := map[int64]*pendingToolCall{}

		for stream.Next() {
			chunk := stream.Value().Data
			if chunk.Error != nil {
				send(margo.Chunk{Err: fmt.Errorf("openrouter: stream error %v: %s", chunk.Error.Code, chunk.Error.Message)})
				return
			}
			if chunk.Usage != nil {
				usage = toUsage(chunk.Usage)
			}
			for _, choice := range chunk.Choices {
				if reasoning, ok := choice.Delta.Reasoning.GetOrZero(); ok && reasoning != "" {
					if !send(margo.Chunk{Kind: margo.ChunkThinking, Text: reasoning}) {
						return
					}
				}
				if content, ok := choice.Delta.Content.GetOrZero(); ok && content != "" {
					if firstToken.IsZero() {
						firstToken = time.Now()
					}
					if !send(margo.Chunk{Kind: margo.ChunkText, Text: content}) {
						return
					}
				}
				for _, tc := range choice.Delta.ToolCalls {
					p, ok := pending[tc.Index]
					if !ok {
						p = &pendingToolCall{}
						pending[tc.Index] = p
					}
					if tc.ID != nil && *tc.ID != "" {
						p.id = *tc.ID
					}
					if tc.Function != nil {
						if tc.Function.Name != nil && *tc.Function.Name != "" {
							p.name = *tc.Function.Name
						}
						if tc.Function.Arguments != nil {
							p.args.WriteString(*tc.Function.Arguments)
						}
					}
				}
			}
		}
		if err := stream.Err(); err != nil {
			send(margo.Chunk{Err: fmt.Errorf("openrouter: %w", err)})
			return
		}

		// Emit fully-assembled tool calls in index order before usage.
		indices := make([]int64, 0, len(pending))
		for i := range pending {
			indices = append(indices, i)
		}
		slices.Sort(indices)
		for _, i := range indices {
			p := pending[i]
			tc := margo.ToolCall{ID: p.id, Name: p.name, Arguments: p.args.String()}
			if !send(margo.Chunk{Kind: margo.ChunkToolCall, ToolCall: &tc}) {
				return
			}
		}

		now := time.Now()
		usage.TotalMs = now.Sub(started).Milliseconds()
		if !firstToken.IsZero() {
			usage.FirstTokenMs = firstToken.Sub(started).Milliseconds()
		}
		send(margo.Chunk{Usage: &usage})
	}()
	return out, nil
}

// toUsage keeps OpenRouter's billed cost alongside the token counts.
func toUsage(u *components.ChatUsage) margo.Usage {
	out := margo.Usage{InputTokens: int(u.PromptTokens), OutputTokens: int(u.CompletionTokens)}
	if cost, ok := u.Cost.Get(); ok && cost != nil {
		out.Cost = cost
	}
	return out
}

// perMTok converts OpenRouter's per-token price string to margo's
// per-million-token float. Returns nil for an absent or unparseable
// value so "rate unknown" stays distinguishable from "free".
func perMTok(s string) *float64 {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	out := v * 1_000_000
	return &out
}

// ListModels fetches OpenRouter's catalog, following pagination. It
// reports everything margo's catalog declares — context window, image
// support, and both token prices — so an OpenRouter model needs no
// entry in the embedded models.json.
func (c *Client) ListModels(ctx context.Context) ([]margo.Model, error) {
	pricedAt := time.Now().UTC().Format("2006-01-02")
	out := []margo.Model{}
	page, err := c.sdk.Models.List(ctx, &operations.GetModelsRequest{}, operations.WithSetHeaders(identityHeaders))
	for page != nil && err == nil {
		for _, m := range page.Result.Data {
			if m.ID == "" {
				continue
			}
			model := margo.Model{
				ID:             m.ID,
				Multimodal:     slices.Contains(m.Architecture.InputModalities, components.InputModalityImage),
				CostPerMTokIn:  perMTok(m.Pricing.Prompt),
				CostPerMTokOut: perMTok(m.Pricing.Completion),
				PricedAt:       pricedAt,
			}
			if m.ContextLength != nil {
				model.ContextTokens = int(*m.ContextLength)
			}
			out = append(out, model)
		}
		page, err = page.Next()
	}
	if err != nil {
		return nil, fmt.Errorf("openrouter: list models: %w", err)
	}
	return out, nil
}
