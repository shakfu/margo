// Package agent integrates margo's chat clients with the Eino orchestration
// framework (github.com/cloudwego/eino).
//
// The Adapter type wraps a margo.Client and exposes it as an Eino
// ToolCallingChatModel, enabling use in chains, graphs, and ReAct agents.
//
// Provider tool support: all three first-party providers (anthropic, openai,
// openrouter) translate margo.Request.Tools into their native tool-calling
// shapes and surface assistant tool calls in margo.Response.ToolCalls /
// streaming chunks. Tool results travel back as margo.Message{Role: RoleTool}
// entries and are batched into a single Anthropic user message containing
// tool_result blocks (or sent as OpenAI tool messages).
package agent

import (
	"context"
	"encoding/json"
	"strings"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/shakfu/margo/pkg/margo"
)

// Adapter exposes a margo.Client as an Eino ToolCallingChatModel.
//
// Provider-level options (model id, system prompt, sampling parameters,
// thinking budget) are captured at construction via Defaults. Per-call
// messages flow in through Generate / Stream from the surrounding Eino flow.
//
// Adapter is safe for concurrent use: WithTools returns a new instance
// rather than mutating the receiver.
type Adapter struct {
	client   margo.Client
	defaults margo.Request
	tools    []margo.ToolDef
	// finalUserAttachments rides on the LAST user-role message converted
	// from the eino schema. This is how multimodal input enters the agent
	// path: schema.Message lacks a multipart slot we can use, so the
	// adapter stamps margo.Parts onto the request just before it leaves.
	// Subsequent React-loop turns (assistant/tool turns) don't carry
	// attachments — only the user's original prompt does.
	finalUserAttachments []margo.Part
}

// NewAdapter returns a chat-model adapter for the given margo client.
//
// `defaults` carries fields that are not part of the per-call message list:
// Model, System, MaxTokens, Temperature, TopP, StopSequences, Thinking,
// ToolChoice. Its Messages and Tools fields are ignored — Eino supplies
// messages via Generate/Stream and tools via WithTools.
func NewAdapter(c margo.Client, defaults margo.Request) *Adapter {
	defaults.Messages = nil
	defaults.Tools = nil
	return &Adapter{client: c, defaults: defaults}
}

// WithFinalUserAttachments returns a new Adapter that, on each request,
// stamps the supplied parts onto the final user-role message before
// shipping. Used by StreamReact to inject image attachments that came
// in via the Wails surface; the parts are independent of WithTools.
func (a *Adapter) WithFinalUserAttachments(parts []margo.Part) *Adapter {
	out := &Adapter{
		client:               a.client,
		defaults:             a.defaults,
		tools:                a.tools,
		finalUserAttachments: parts,
	}
	return out
}

// Compile-time assertions.
var (
	_ einomodel.BaseChatModel        = (*Adapter)(nil)
	_ einomodel.ToolCallingChatModel = (*Adapter)(nil)
)

// WithTools returns a new Adapter with the given tools bound. The receiver
// is not modified, so a single base adapter can be safely shared across
// goroutines and derived per-request with different tool sets.
func (a *Adapter) WithTools(tools []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	out := &Adapter{
		client:               a.client,
		defaults:             a.defaults,
		finalUserAttachments: a.finalUserAttachments,
	}
	if len(tools) > 0 {
		converted := make([]margo.ToolDef, 0, len(tools))
		for _, t := range tools {
			td, err := toolInfoToDef(t)
			if err != nil {
				return nil, err
			}
			converted = append(converted, td)
		}
		out.tools = converted
	}
	return out, nil
}

func toolInfoToDef(t *schema.ToolInfo) (margo.ToolDef, error) {
	def := margo.ToolDef{Name: t.Name, Description: t.Desc}
	if t.ParamsOneOf == nil {
		return def, nil
	}
	js, err := t.ParamsOneOf.ToJSONSchema()
	if err != nil {
		return margo.ToolDef{}, err
	}
	if js == nil {
		return def, nil
	}
	// Round-trip via JSON to normalize into a map[string]any that providers
	// can pass to their SDKs without depending on the jsonschema package.
	raw, err := json.Marshal(js)
	if err != nil {
		return margo.ToolDef{}, err
	}
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return margo.ToolDef{}, err
	}
	def.Parameters = params
	return def, nil
}

func (a *Adapter) request(input []*schema.Message) margo.Request {
	req := a.defaults
	req.Messages = nil
	req.Tools = a.tools

	var sys []string
	if req.System != "" {
		sys = append(sys, req.System)
	}
	for _, m := range input {
		if m == nil {
			continue
		}
		switch m.Role {
		case schema.System:
			if m.Content != "" {
				sys = append(sys, m.Content)
			}
		case schema.User:
			req.Messages = append(req.Messages, margo.Message{Role: margo.RoleUser, Content: m.Content})
		case schema.Assistant:
			msg := margo.Message{Role: margo.RoleAssistant, Content: m.Content}
			for _, tc := range m.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, margo.ToolCall{
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})
			}
			req.Messages = append(req.Messages, msg)
		case schema.Tool:
			req.Messages = append(req.Messages, margo.Message{
				Role:       margo.RoleTool,
				Content:    m.Content,
				ToolCallID: m.ToolCallID,
			})
		}
	}
	req.System = strings.Join(sys, "\n\n")

	// Stamp attachments onto the LAST user-role message. Subsequent
	// React-loop turns (assistant or tool messages) don't carry parts.
	if len(a.finalUserAttachments) > 0 {
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == margo.RoleUser {
				m := req.Messages[i]
				parts := make([]margo.Part, 0, len(a.finalUserAttachments)+1)
				if m.Content != "" {
					parts = append(parts, margo.Part{Kind: margo.PartText, Text: m.Content})
				}
				parts = append(parts, a.finalUserAttachments...)
				m.Parts = parts
				req.Messages[i] = m
				break
			}
		}
	}
	return req
}

// Generate runs a non-streaming completion.
func (a *Adapter) Generate(ctx context.Context, input []*schema.Message, _ ...einomodel.Option) (*schema.Message, error) {
	resp, err := a.client.Complete(ctx, a.request(input))
	if err != nil {
		return nil, err
	}
	out := &schema.Message{
		Role:             schema.Assistant,
		Content:          resp.Text,
		ReasoningContent: resp.Thinking,
	}
	if len(resp.ToolCalls) > 0 {
		out.ToolCalls = make([]schema.ToolCall, 0, len(resp.ToolCalls))
		for i, tc := range resp.ToolCalls {
			idx := i
			out.ToolCalls = append(out.ToolCalls, schema.ToolCall{
				Index: &idx,
				ID:    tc.ID,
				Type:  "function",
				Function: schema.FunctionCall{
					Name:      tc.Name,
					Arguments: tc.Arguments,
				},
			})
		}
	}
	if resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0 || len(resp.ToolCalls) > 0 {
		meta := &schema.ResponseMeta{}
		if len(resp.ToolCalls) > 0 {
			meta.FinishReason = "tool_calls"
		}
		if resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0 {
			meta.Usage = &schema.TokenUsage{
				PromptTokens:     resp.Usage.InputTokens,
				CompletionTokens: resp.Usage.OutputTokens,
				TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
			}
		}
		out.ResponseMeta = meta
	}
	return out, nil
}

// Stream runs a streaming completion. Each emitted chunk is a partial
// assistant message; Eino concatenates them via schema.ConcatMessages.
//
// Tool calls are accumulated by the underlying provider and emitted as
// fully-assembled ChunkToolCall entries before the final usage chunk; the
// adapter forwards each as a single-element ToolCalls message.
func (a *Adapter) Stream(ctx context.Context, input []*schema.Message, _ ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	ch, err := a.client.Stream(ctx, a.request(input))
	if err != nil {
		return nil, err
	}

	reader, writer := schema.Pipe[*schema.Message](16)
	go func() {
		defer writer.Close()
		toolIndex := 0
		for chunk := range ch {
			if chunk.Err != nil {
				writer.Send(nil, chunk.Err)
				return
			}
			if chunk.Usage != nil {
				writer.Send(&schema.Message{
					Role: schema.Assistant,
					ResponseMeta: &schema.ResponseMeta{
						Usage: &schema.TokenUsage{
							PromptTokens:     chunk.Usage.InputTokens,
							CompletionTokens: chunk.Usage.OutputTokens,
							TotalTokens:      chunk.Usage.InputTokens + chunk.Usage.OutputTokens,
						},
					},
				}, nil)
				continue
			}
			if chunk.Kind == margo.ChunkToolCall && chunk.ToolCall != nil {
				idx := toolIndex
				toolIndex++
				msg := &schema.Message{
					Role: schema.Assistant,
					ToolCalls: []schema.ToolCall{{
						Index: &idx,
						ID:    chunk.ToolCall.ID,
						Type:  "function",
						Function: schema.FunctionCall{
							Name:      chunk.ToolCall.Name,
							Arguments: chunk.ToolCall.Arguments,
						},
					}},
					ResponseMeta: &schema.ResponseMeta{FinishReason: "tool_calls"},
				}
				if writer.Send(msg, nil) {
					return
				}
				continue
			}
			msg := &schema.Message{Role: schema.Assistant}
			if chunk.Kind == margo.ChunkThinking {
				msg.ReasoningContent = chunk.Text
			} else {
				msg.Content = chunk.Text
			}
			if writer.Send(msg, nil) {
				return
			}
		}
	}()
	return reader, nil
}
