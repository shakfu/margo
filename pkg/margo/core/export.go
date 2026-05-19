package core

import (
	"fmt"
	"strings"
)

// ChatExport is the payload passed to RenderChatMarkdown. It is a
// transport-agnostic representation of a chat suitable for serialization
// from any frontend — Wails serializes from its localStorage Chat shape,
// the TUI will eventually serialize from its in-memory transcript.
// Fields are deliberately denormalized (PersonaName / AgentName as
// strings rather than ids) so the renderer needs no lookup tables.
type ChatExport struct {
	Title       string             `json:"title"`
	Provider    string             `json:"provider"`
	Model       string             `json:"model"`
	PersonaName string             `json:"personaName,omitempty"`
	AgentName   string             `json:"agentName,omitempty"`
	CreatedAt   string             `json:"createdAt,omitempty"` // ISO 8601 or any human string
	UpdatedAt   string             `json:"updatedAt,omitempty"`
	TokensIn    int                `json:"tokensIn,omitempty"`
	TokensOut   int                `json:"tokensOut,omitempty"`
	Messages    []ExportMessage    `json:"messages"`
}

// ExportMessage is one turn in the chat. Content is rendered verbatim
// (already markdown in practice — the chat UI feeds markdown into the
// model and the model's responses are markdown). Thinking is wrapped in
// a <details> block so the export reads cleanly without it but the
// content is still recoverable.
type ExportMessage struct {
	Role        string             `json:"role"`
	Content     string             `json:"content"`
	Thinking    string             `json:"thinking,omitempty"`
	Attachments []ExportAttachment `json:"attachments,omitempty"`
	Steps       []ExportStep       `json:"steps,omitempty"`
}

// ExportAttachment describes one attachment that rode with a message.
// The renderer surfaces name/mime/size; it does not embed the bytes
// (markdown export is intentionally text-only — a future "export as
// zip" mode could bundle attachments alongside).
type ExportAttachment struct {
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	Size     int64  `json:"size"`
}

// ExportStep mirrors an AgentStep flattened for export. Renderer turns
// the kind+name+arguments+result quartet into a compact summary; full
// tool-call payloads can be voluminous, so a "compact" rendering is
// the right default. Verbose mode is a future option.
type ExportStep struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
	Result    string `json:"result,omitempty"`
	IsError   bool   `json:"isError,omitempty"`
}

// RenderChatMarkdown returns a single markdown document representing the
// chat. The output is deterministic, h1-rooted, and safe to round-trip
// through any markdown renderer (CommonMark + GFM). It does not embed
// images or binary attachments — those are listed by name only.
//
// Format outline:
//
//	# <Title>
//
//	> **Provider:** ... · **Model:** ... · **Tokens:** ...  · **Persona/Agent:** ...
//	> **Created:** ... · **Updated:** ...
//
//	---
//
//	### You
//
//	<content>
//
//	**Attached:** `name` (mime, size)
//
//	---
//
//	### Assistant
//
//	<content>
//
//	<details><summary>Thinking</summary>
//
//	<thinking>
//
//	</details>
//
//	**Tools used:**
//	- `tool_name` → <truncated result>
func RenderChatMarkdown(c ChatExport) string {
	var b strings.Builder

	title := strings.TrimSpace(c.Title)
	if title == "" {
		title = "Untitled chat"
	}
	fmt.Fprintf(&b, "# %s\n\n", title)

	// Metadata block. Pieces are joined with " · " so partial metadata
	// (missing tokens, missing persona) still renders cleanly. The
	// blockquote keeps the metadata visually separate from the chat body
	// when rendered.
	meta := []string{}
	if c.Provider != "" {
		meta = append(meta, "**Provider:** "+c.Provider)
	}
	if c.Model != "" {
		meta = append(meta, "**Model:** `"+c.Model+"`")
	}
	if c.PersonaName != "" {
		meta = append(meta, "**Persona:** "+c.PersonaName)
	}
	if c.AgentName != "" {
		meta = append(meta, "**Agent:** "+c.AgentName)
	}
	if c.TokensIn > 0 || c.TokensOut > 0 {
		meta = append(meta, fmt.Sprintf("**Tokens:** %d↑ %d↓", c.TokensIn, c.TokensOut))
	}
	if len(meta) > 0 {
		fmt.Fprintf(&b, "> %s\n", strings.Join(meta, " · "))
	}
	if c.CreatedAt != "" || c.UpdatedAt != "" {
		dates := []string{}
		if c.CreatedAt != "" {
			dates = append(dates, "**Created:** "+c.CreatedAt)
		}
		if c.UpdatedAt != "" && c.UpdatedAt != c.CreatedAt {
			dates = append(dates, "**Updated:** "+c.UpdatedAt)
		}
		fmt.Fprintf(&b, "> %s\n", strings.Join(dates, " · "))
	}
	b.WriteString("\n---\n\n")

	for i, m := range c.Messages {
		renderExportMessage(&b, m)
		if i < len(c.Messages)-1 {
			b.WriteString("---\n\n")
		}
	}

	return b.String()
}

func renderExportMessage(b *strings.Builder, m ExportMessage) {
	heading := strings.Title(m.Role)
	if m.Role == "user" {
		heading = "You"
	} else if m.Role == "assistant" {
		heading = "Assistant"
	}
	fmt.Fprintf(b, "### %s\n\n", heading)

	content := strings.TrimSpace(m.Content)
	if content != "" {
		b.WriteString(content)
		b.WriteString("\n\n")
	}

	if len(m.Attachments) > 0 {
		b.WriteString("**Attached:**\n")
		for _, a := range m.Attachments {
			fmt.Fprintf(b, "- `%s` (%s, %s)\n", a.Name, a.MimeType, humanSize(a.Size))
		}
		b.WriteString("\n")
	}

	if thinking := strings.TrimSpace(m.Thinking); thinking != "" {
		b.WriteString("<details><summary>Thinking</summary>\n\n")
		b.WriteString(thinking)
		b.WriteString("\n\n</details>\n\n")
	}

	if len(m.Steps) > 0 {
		b.WriteString("**Tools used:**\n")
		for _, s := range m.Steps {
			renderExportStep(b, s)
		}
		b.WriteString("\n")
	}
}

// renderExportStep emits one line per step. Permission events are
// summarised by approval status (we don't know the user's decision
// here — that's on the upstream caller to translate into kind=tool_call
// before exporting). tool_stream chunks aren't surfaced; the matching
// tool_result has the same final text.
func renderExportStep(b *strings.Builder, s ExportStep) {
	switch s.Kind {
	case "tool_call":
		args := compactOneLine(s.Arguments, 80)
		fmt.Fprintf(b, "- `%s(%s)`\n", s.Name, args)
	case "tool_result":
		body := compactOneLine(s.Result, 200)
		marker := "→"
		if s.IsError {
			marker = "✗"
		}
		if s.Name != "" {
			fmt.Fprintf(b, "  %s %s\n", marker, body)
		} else {
			fmt.Fprintf(b, "- %s %s\n", marker, body)
		}
	case "permission":
		fmt.Fprintf(b, "- `%s` (permission requested)\n", s.Name)
	case "tool_retrieve":
		fmt.Fprintf(b, "- `%s` (retrieval)\n", s.Name)
	}
}

// compactOneLine flattens whitespace and truncates with an ellipsis. The
// argument and result fields can hold multi-KB JSON blobs in agent runs;
// a markdown export shouldn't paste them verbatim.
func compactOneLine(s string, max int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	// Collapse runs of whitespace.
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// humanSize formats a byte count with one decimal of K/M/G prefix.
func humanSize(n int64) string {
	const k = 1024
	switch {
	case n < k:
		return fmt.Sprintf("%d B", n)
	case n < k*k:
		return fmt.Sprintf("%.1f KB", float64(n)/k)
	case n < k*k*k:
		return fmt.Sprintf("%.1f MB", float64(n)/(k*k))
	default:
		return fmt.Sprintf("%.1f GB", float64(n)/(k*k*k))
	}
}
