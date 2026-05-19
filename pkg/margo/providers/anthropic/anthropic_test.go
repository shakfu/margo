package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

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
		defaultModel: "claude-haiku-4-5",
	}
}

// jsonReply is a small helper that writes a JSON object as the response.
func jsonReply(w http.ResponseWriter, obj any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(obj)
}

// sseReply writes a sequence of (event, data-JSON) pairs in Anthropic's
// SSE format: `event: <name>\ndata: <json>\n\n`. Each pair is one SSE
// frame; the SDK parses them into typed stream events.
func sseReply(w http.ResponseWriter, events []sseFrame) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	for _, ev := range events {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.event, ev.data)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

type sseFrame struct{ event, data string }

// TestCompleteParsesTextAndUsage verifies the happy-path non-streaming
// completion: text content block flows into Response.Text; usage parses
// into the Usage struct. Confirms the SDK's response shape hasn't drifted
// from what our adapter expects.
func TestCompleteParsesTextAndUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, map[string]any{
			"id":      "msg_01",
			"type":    "message",
			"role":    "assistant",
			"model":   "claude-haiku-4-5",
			"content": []map[string]any{{"type": "text", "text": "Hello world"}},
			"stop_reason": "end_turn",
			"usage":       map[string]int{"input_tokens": 42, "output_tokens": 3},
		})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	resp, err := c.Complete(context.Background(), margo.Request{
		Model:    "claude-haiku-4-5",
		Messages: []margo.Message{{Role: margo.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Text != "Hello world" {
		t.Errorf("Text = %q, want %q", resp.Text, "Hello world")
	}
	if resp.Usage.InputTokens != 42 || resp.Usage.OutputTokens != 3 {
		t.Errorf("Usage = %+v, want {42 3 ...}", resp.Usage)
	}
}

// TestCompleteParsesToolUse exercises the tool-call decoding path. The
// adapter must recover ID / Name / Arguments from a tool_use content
// block. ToolCalls is what the agent runner reads to dispatch.
func TestCompleteParsesToolUse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, map[string]any{
			"id":    "msg_02",
			"type":  "message",
			"role":  "assistant",
			"model": "claude-haiku-4-5",
			"content": []map[string]any{
				{"type": "text", "text": "Looking up the time."},
				{"type": "tool_use", "id": "toolu_1", "name": "current_time",
					"input": map[string]any{"timezone": "UTC"}},
			},
			"stop_reason": "tool_use",
			"usage":       map[string]int{"input_tokens": 50, "output_tokens": 8},
		})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	resp, err := c.Complete(context.Background(), margo.Request{
		Model:    "claude-haiku-4-5",
		Messages: []margo.Message{{Role: margo.RoleUser, Content: "what time is it?"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d: %+v", len(resp.ToolCalls), resp.ToolCalls)
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "toolu_1" || tc.Name != "current_time" {
		t.Errorf("ToolCall = %+v", tc)
	}
	// Arguments is the JSON-encoded input. Decode for a stable comparison
	// (the SDK normalizes encoding so a string match could flake).
	var args map[string]string
	if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
		t.Fatalf("ToolCall.Arguments not valid JSON: %v\n%s", err, tc.Arguments)
	}
	if args["timezone"] != "UTC" {
		t.Errorf("Arguments = %v, want timezone=UTC", args)
	}
}

// TestCompleteSendsMultimodalParts decodes the outbound request body and
// asserts the multimodal user message included both a text and an image
// block. Catches regressions in toAnthropicUserBlocks (§7.5).
func TestCompleteSendsMultimodalParts(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		jsonReply(w, map[string]any{
			"id": "msg", "type": "message", "role": "assistant", "model": "claude-haiku-4-5",
			"content": []map[string]any{{"type": "text", "text": "ok"}},
			"stop_reason": "end_turn",
			"usage": map[string]int{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, err := c.Complete(context.Background(), margo.Request{
		Model: "claude-haiku-4-5",
		Messages: []margo.Message{{
			Role:    margo.RoleUser,
			Content: "what's in this?",
			Parts: []margo.Part{
				{Kind: margo.PartText, Text: "what's in this?"},
				{Kind: margo.PartImage, MimeType: "image/png", Data: []byte("PNGBYTES")},
			},
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	msgs, _ := captured["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatalf("no messages in request body: %v", captured)
	}
	first, _ := msgs[0].(map[string]any)
	blocks, _ := first["content"].([]any)
	var foundImage bool
	for _, b := range blocks {
		bm, _ := b.(map[string]any)
		if bm["type"] == "image" {
			foundImage = true
			src, _ := bm["source"].(map[string]any)
			if src["type"] != "base64" || src["media_type"] != "image/png" {
				t.Errorf("image source malformed: %+v", src)
			}
		}
	}
	if !foundImage {
		t.Errorf("outbound multimodal request missing image block: %+v", blocks)
	}
}

// TestStreamYieldsTextDeltas drives the SSE decoder with a minimal event
// sequence: message_start → content_block_start (text) →
// content_block_delta (text_delta) × N → content_block_stop →
// message_delta → message_stop. Asserts the channel emits exactly the
// expected text chunks plus a usage chunk at the tail.
func TestStreamYieldsTextDeltas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseReply(w, []sseFrame{
			{"message_start", `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"claude-haiku-4-5","content":[],"stop_reason":null,"usage":{"input_tokens":7,"output_tokens":0}}}`},
			{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`},
			{"message_stop", `{"type":"message_stop"}`},
		})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	ch, err := c.Stream(context.Background(), margo.Request{
		Model:    "claude-haiku-4-5",
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

// TestStreamYieldsToolCall verifies that a tool_use streamed across
// content_block_start + input_json_delta + content_block_stop emits a
// single ChunkToolCall on the channel. This is the path that lets the
// agent runner observe the model's tool-call decision in-flight.
func TestStreamYieldsToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseReply(w, []sseFrame{
			{"message_start", `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"claude-haiku-4-5","content":[],"usage":{"input_tokens":5,"output_tokens":0}}}`},
			{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"current_time","input":{}}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"timezone\":\"UTC\"}"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":4}}`},
			{"message_stop", `{"type":"message_stop"}`},
		})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	ch, err := c.Stream(context.Background(), margo.Request{
		Model:    "claude-haiku-4-5",
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
	if gotTool.ID != "toolu_1" || gotTool.Name != "current_time" {
		t.Errorf("tool call identity wrong: %+v", *gotTool)
	}
	// Arguments accumulates as partial_json deltas; the final string
	// should parse cleanly with timezone=UTC.
	var args map[string]string
	if err := json.Unmarshal([]byte(gotTool.Arguments), &args); err != nil {
		t.Fatalf("tool args not JSON: %v\n%q", err, gotTool.Arguments)
	}
	if args["timezone"] != "UTC" {
		t.Errorf("tool args = %v, want timezone=UTC", args)
	}
}

// TestStreamSurfacesHTTPError checks that a non-2xx upstream is surfaced
// as either an error from Stream() or as an Err chunk on the channel.
// Both are acceptable contract points; the test accepts either so a
// future SDK upgrade that changes the error timing doesn't false-fail.
func TestStreamSurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"boom"}}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	ch, err := c.Stream(context.Background(), margo.Request{
		Model:    "claude-haiku-4-5",
		Messages: []margo.Message{{Role: margo.RoleUser, Content: "hi"}},
	})
	if err != nil {
		// Surface-at-call-site is acceptable.
		return
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
