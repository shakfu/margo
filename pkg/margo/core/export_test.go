package core

import (
	"strings"
	"testing"
)

func TestRenderChatMarkdownMinimal(t *testing.T) {
	c := ChatExport{
		Title:    "Sample",
		Provider: "anthropic",
		Model:    "claude-opus-4-7",
		Messages: []ExportMessage{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there"},
		},
	}
	got := RenderChatMarkdown(c)

	must := []string{
		"# Sample",
		"**Provider:** anthropic",
		"**Model:** `claude-opus-4-7`",
		"### You",
		"Hello",
		"### Assistant",
		"Hi there",
		"---",
	}
	for _, m := range must {
		if !strings.Contains(got, m) {
			t.Errorf("expected output to contain %q\n----\n%s", m, got)
		}
	}
}

func TestRenderChatMarkdownThinkingAndAttachments(t *testing.T) {
	c := ChatExport{
		Title: "With extras",
		Messages: []ExportMessage{
			{
				Role:    "user",
				Content: "Look at this",
				Attachments: []ExportAttachment{
					{Name: "doc.pdf", MimeType: "application/pdf", Size: 1500},
				},
			},
			{
				Role:     "assistant",
				Content:  "Here.",
				Thinking: "let me think about it",
			},
		},
	}
	got := RenderChatMarkdown(c)
	if !strings.Contains(got, "`doc.pdf` (application/pdf, 1.5 KB)") {
		t.Errorf("attachment line missing or malformed:\n%s", got)
	}
	if !strings.Contains(got, "<details><summary>Thinking</summary>") {
		t.Errorf("thinking should be wrapped in details block:\n%s", got)
	}
	if !strings.Contains(got, "let me think about it") {
		t.Errorf("thinking content missing:\n%s", got)
	}
}

func TestRenderChatMarkdownAgentSteps(t *testing.T) {
	c := ChatExport{
		Title: "Agent run",
		Messages: []ExportMessage{
			{Role: "user", Content: "fetch the page"},
			{
				Role:    "assistant",
				Content: "Done.",
				Steps: []ExportStep{
					{Kind: "tool_call", Name: "web_fetch", Arguments: `{"url":"https://example.com"}`},
					{Kind: "tool_result", Name: "web_fetch", Result: "Example Domain..."},
				},
			},
		},
	}
	got := RenderChatMarkdown(c)
	if !strings.Contains(got, "**Tools used:**") {
		t.Errorf("missing tools section:\n%s", got)
	}
	if !strings.Contains(got, "`web_fetch(") {
		t.Errorf("tool call line missing:\n%s", got)
	}
	if !strings.Contains(got, "→ Example Domain") {
		t.Errorf("tool result line missing:\n%s", got)
	}
}

func TestRenderChatMarkdownUntitled(t *testing.T) {
	got := RenderChatMarkdown(ChatExport{})
	if !strings.HasPrefix(got, "# Untitled chat") {
		t.Errorf("empty chat should default to Untitled chat:\n%s", got)
	}
}

func TestCompactOneLineTruncates(t *testing.T) {
	s := strings.Repeat("x", 300)
	got := compactOneLine(s, 50)
	if len(got) > 51 { // 50 + ellipsis rune (3 bytes in UTF-8)
		// The ellipsis is "…" — three bytes; allow some slack.
		if !strings.HasSuffix(got, "…") {
			t.Errorf("compactOneLine should truncate with ellipsis, got %q", got)
		}
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{
		512:           "512 B",
		2048:          "2.0 KB",
		5 * 1024 * 1024: "5.0 MB",
	}
	for in, want := range cases {
		if got := humanSize(in); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", in, got, want)
		}
	}
}
