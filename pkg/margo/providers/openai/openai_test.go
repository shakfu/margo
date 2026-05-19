package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/shakfu/margo/pkg/margo"
)

// newTestClient builds a Client wired to a test http server. Bypasses
// New() because tests need to inject WithBaseURL; the package's
// public surface keeps API keys mandatory.
func newTestClient(serverURL string) *Client {
	return &Client{
		sdk: sdk.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(serverURL),
		),
		defaultModel: "gpt-5.4-nano",
	}
}

// jsonReply writes a JSON object as the response.
func jsonReply(w http.ResponseWriter, obj any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(obj)
}

// sseReply writes a sequence of OpenAI-style `data: <json>` frames
// terminated by the `data: [DONE]` sentinel. OpenAI's SSE is "no event
// names, single data line per frame" — the inverse of Anthropic's.
func sseReply(w http.ResponseWriter, frames []string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	for _, data := range frames {
		fmt.Fprintf(w, "data: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// TestCompleteParsesTextAndUsage — non-streaming happy path. Confirms
// the SDK's chat-completion response decodes into margo.Response with
// text content and usage tokens preserved.
func TestCompleteParsesTextAndUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, map[string]any{
			"id":      "chatcmpl-1",
			"object":  "chat.completion",
			"created": 1700000000,
			"model":   "gpt-5.4-nano",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "Hello world"},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 11, "completion_tokens": 2, "total_tokens": 13},
		})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	resp, err := c.Complete(context.Background(), margo.Request{
		Model:    "gpt-5.4-nano",
		Messages: []margo.Message{{Role: margo.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Text != "Hello world" {
		t.Errorf("Text = %q, want %q", resp.Text, "Hello world")
	}
	if resp.Usage.InputTokens != 11 || resp.Usage.OutputTokens != 2 {
		t.Errorf("Usage = %+v, want {11 2 ...}", resp.Usage)
	}
}

// TestCompleteParsesToolCalls — recovers ID / Name / Arguments from a
// function tool call. The agent runner reads ToolCalls to dispatch.
func TestCompleteParsesToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, map[string]any{
			"id": "chatcmpl-2", "object": "chat.completion", "model": "gpt-5.4-nano",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "",
					"tool_calls": []map[string]any{{
						"id": "call_1", "type": "function",
						"function": map[string]any{"name": "current_time", "arguments": `{"timezone":"UTC"}`},
					}},
				},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]int{"prompt_tokens": 20, "completion_tokens": 6},
		})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	resp, err := c.Complete(context.Background(), margo.Request{
		Model:    "gpt-5.4-nano",
		Messages: []margo.Message{{Role: margo.RoleUser, Content: "time?"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "current_time" {
		t.Errorf("ToolCall = %+v", tc)
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
		t.Fatalf("args not JSON: %v\n%s", err, tc.Arguments)
	}
	if args["timezone"] != "UTC" {
		t.Errorf("args = %v", args)
	}
}

// TestCompleteSendsImagePart asserts the outbound request includes an
// image_url content part when the user message has an image attachment.
// Catches regressions in toSDKUserParts (§7.5).
func TestCompleteSendsImagePart(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		jsonReply(w, map[string]any{
			"id": "c", "object": "chat.completion", "model": "gpt-5.4-nano",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{"role": "assistant", "content": "ok"},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, err := c.Complete(context.Background(), margo.Request{
		Model: "gpt-5.4-nano",
		Messages: []margo.Message{{
			Role:    margo.RoleUser,
			Content: "describe",
			Parts: []margo.Part{
				{Kind: margo.PartText, Text: "describe"},
				{Kind: margo.PartImage, MimeType: "image/png", Data: []byte("PNGBYTES")},
			},
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	msgs, _ := captured["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatalf("no messages in request: %v", captured)
	}
	user, _ := msgs[len(msgs)-1].(map[string]any)
	parts, _ := user["content"].([]any)
	var foundImage bool
	for _, p := range parts {
		pm, _ := p.(map[string]any)
		if pm["type"] == "image_url" {
			foundImage = true
			imgURL, _ := pm["image_url"].(map[string]any)
			url, _ := imgURL["url"].(string)
			if !strings.HasPrefix(url, "data:image/png;base64,") {
				t.Errorf("image data URL malformed: %q", url)
			}
		}
	}
	if !foundImage {
		t.Errorf("outbound multimodal request missing image_url part: %+v", parts)
	}
}

// TestStreamYieldsTextDeltas drives the SSE decoder with an OpenAI chunk
// sequence: a series of `delta.content` frames followed by a usage-only
// frame and a `[DONE]` sentinel. Asserts text reassembly + usage.
func TestStreamYieldsTextDeltas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseReply(w, []string{
			`{"id":"c","object":"chat.completion.chunk","model":"gpt-5.4-nano","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
			`{"id":"c","object":"chat.completion.chunk","model":"gpt-5.4-nano","choices":[{"index":0,"delta":{"content":"Hel"}}]}`,
			`{"id":"c","object":"chat.completion.chunk","model":"gpt-5.4-nano","choices":[{"index":0,"delta":{"content":"lo"}}]}`,
			`{"id":"c","object":"chat.completion.chunk","model":"gpt-5.4-nano","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`{"id":"c","object":"chat.completion.chunk","model":"gpt-5.4-nano","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}`,
		})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	ch, err := c.Stream(context.Background(), margo.Request{
		Model:    "gpt-5.4-nano",
		Messages: []margo.Message{{Role: margo.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var text strings.Builder
	var sawUsage bool
	for ch := range ch {
		if ch.Err != nil {
			t.Fatalf("chunk error: %v", ch.Err)
		}
		if ch.Usage != nil {
			sawUsage = true
			if ch.Usage.InputTokens != 7 || ch.Usage.OutputTokens != 2 {
				t.Errorf("Usage = %+v, want {7 2 ...}", *ch.Usage)
			}
			continue
		}
		if ch.Kind == margo.ChunkText {
			text.WriteString(ch.Text)
		}
	}
	if text.String() != "Hello" {
		t.Errorf("streamed text = %q, want %q", text.String(), "Hello")
	}
	if !sawUsage {
		t.Errorf("stream did not emit a usage chunk at end")
	}
}

// TestStreamYieldsToolCall checks that delta-based tool-call accumulation
// emits a single ChunkToolCall with the fully-assembled arguments. Each
// frame carries a fragment of the JSON; concatenation must produce
// parseable JSON at end.
func TestStreamYieldsToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseReply(w, []string{
			`{"id":"c","object":"chat.completion.chunk","model":"gpt-5.4-nano","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"current_time","arguments":""}}]}}]}`,
			`{"id":"c","object":"chat.completion.chunk","model":"gpt-5.4-nano","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"timezone\":"}}]}}]}`,
			`{"id":"c","object":"chat.completion.chunk","model":"gpt-5.4-nano","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"UTC\"}"}}]}}]}`,
			`{"id":"c","object":"chat.completion.chunk","model":"gpt-5.4-nano","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			`{"id":"c","object":"chat.completion.chunk","model":"gpt-5.4-nano","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`,
		})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	ch, err := c.Stream(context.Background(), margo.Request{
		Model:    "gpt-5.4-nano",
		Messages: []margo.Message{{Role: margo.RoleUser, Content: "what time?"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var gotTool *margo.ToolCall
	for ch := range ch {
		if ch.Err != nil {
			t.Fatalf("chunk error: %v", ch.Err)
		}
		if ch.Kind == margo.ChunkToolCall && ch.ToolCall != nil {
			tc := *ch.ToolCall
			gotTool = &tc
		}
	}
	if gotTool == nil {
		t.Fatalf("no tool-call chunk emitted")
	}
	if gotTool.ID != "call_1" || gotTool.Name != "current_time" {
		t.Errorf("tool call identity wrong: %+v", *gotTool)
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(gotTool.Arguments), &args); err != nil {
		t.Fatalf("tool args not JSON: %v\n%q", err, gotTool.Arguments)
	}
	if args["timezone"] != "UTC" {
		t.Errorf("args = %v, want timezone=UTC", args)
	}
}

// TestStreamSurfacesHTTPError verifies a non-2xx upstream is surfaced
// as either an error from Stream() or an Err chunk on the channel.
func TestStreamSurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom","type":"server_error"}}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	ch, err := c.Stream(context.Background(), margo.Request{
		Model:    "gpt-5.4-nano",
		Messages: []margo.Message{{Role: margo.RoleUser, Content: "hi"}},
	})
	if err != nil {
		return // surface at call site is acceptable
	}
	var sawErr bool
	for ch := range ch {
		if ch.Err != nil {
			sawErr = true
		}
	}
	if !sawErr {
		t.Errorf("expected an error from a 500 upstream, got none")
	}
}
