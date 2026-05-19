package core

import (
	"github.com/cloudwego/eino/schema"

	"github.com/shakfu/margo/pkg/margo"
	"github.com/shakfu/margo/pkg/margo/agent"
)

// toMargoMessages translates the public Message slice into the provider-
// facing margo.Message slice. Unknown role strings collapse to user.
func toMargoMessages(in []Message) []margo.Message {
	out := make([]margo.Message, len(in))
	for i, m := range in {
		role := margo.RoleUser
		if m.Role == "assistant" {
			role = margo.RoleAssistant
		}
		out[i] = margo.Message{Role: role, Content: m.Content}
	}
	return out
}

// attachmentsToParts converts core.Attachment values to margo.Part values
// for the agent path, which threads parts through the adapter rather than
// mutating the request's Messages. Empty / malformed entries are dropped.
func attachmentsToParts(in []Attachment) []margo.Part {
	if len(in) == 0 {
		return nil
	}
	out := make([]margo.Part, 0, len(in))
	for _, a := range in {
		if len(a.Data) == 0 || a.MimeType == "" {
			continue
		}
		kind := margo.PartDocument
		if isImageMime(a.MimeType) {
			kind = margo.PartImage
		}
		out = append(out, margo.Part{Kind: kind, MimeType: a.MimeType, Data: a.Data, Name: a.Name})
	}
	return out
}

// applyAttachments glues attachments onto the final user-role message's
// Parts. The original Content string is preserved as a leading text part
// so the prompt and the binary blob both reach the model in the same turn.
func applyAttachments(msgs []margo.Message, attachments []Attachment) []margo.Message {
	if len(attachments) == 0 || len(msgs) == 0 {
		return msgs
	}
	idx := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == margo.RoleUser {
			idx = i
			break
		}
	}
	if idx < 0 {
		return msgs
	}
	target := msgs[idx]
	parts := make([]margo.Part, 0, len(attachments)+1)
	if target.Content != "" {
		parts = append(parts, margo.Part{Kind: margo.PartText, Text: target.Content})
	}
	for _, a := range attachments {
		if len(a.Data) == 0 || a.MimeType == "" {
			continue
		}
		kind := margo.PartDocument
		if isImageMime(a.MimeType) {
			kind = margo.PartImage
		}
		parts = append(parts, margo.Part{Kind: kind, MimeType: a.MimeType, Data: a.Data, Name: a.Name})
	}
	target.Parts = parts
	msgs[idx] = target
	return msgs
}

func isImageMime(m string) bool {
	return len(m) >= 6 && m[:6] == "image/"
}

// toMargoRequest assembles the provider-facing margo.Request, including
// the Chat-Manager budget rewrite when a model is selected.
func toMargoRequest(system string, messages []Message, opts Options, attachments []Attachment) margo.Request {
	msgs := toMargoMessages(messages)
	msgs = applyAttachments(msgs, attachments)
	if opts.Model != "" {
		msgs = agent.RewriteMargoForBudget(msgs, system, agent.BudgetForModel(opts.Model))
	}
	req := margo.Request{
		Model:         opts.Model,
		System:        system,
		Messages:      msgs,
		MaxTokens:     opts.MaxTokens,
		Temperature:   opts.Temperature,
		TopP:          opts.TopP,
		StopSequences: opts.StopSequences,
	}
	if opts.ThinkEnabled {
		req.Thinking = &margo.Thinking{Enabled: true, BudgetTokens: opts.ThinkBudget}
	}
	return req
}

// toSchemaMessages translates Message values into eino schema messages
// for the agent runners. Unknown roles collapse to user; "system" maps
// through verbatim.
func toSchemaMessages(in []Message) []*schema.Message {
	out := make([]*schema.Message, 0, len(in))
	for _, m := range in {
		role := schema.User
		switch m.Role {
		case "assistant":
			role = schema.Assistant
		case "system":
			role = schema.System
		}
		out = append(out, &schema.Message{Role: role, Content: m.Content})
	}
	return out
}
