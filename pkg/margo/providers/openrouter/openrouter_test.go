package openrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenRouterTeam/go-sdk/retry"

	"github.com/shakfu/margo/pkg/margo"
)

// newTestClient points a client at a test server with retries off, so
// error tests fail fast. TestRetriesServerErrors covers retryPolicy.
func newTestClient(serverURL string) *Client {
	return newClient("test-key", serverURL, retry.Config{Strategy: "none"})
}

func jsonReply(w http.ResponseWriter, obj any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(obj)
}

// sseReply writes `data: <json>` frames terminated by `data: [DONE]`.
func sseReply(w http.ResponseWriter, frames []string) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	for _, data := range frames {
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}

func completion(content string) map[string]any {
	return map[string]any{
		"id": "c", "object": "chat.completion", "created": 1, "model": "deepseek/deepseek-v3.2",
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 4, "total_tokens": 9, "cost": 0.00042},
	}
}

func hi() margo.Request {
	return margo.Request{Messages: []margo.Message{{Role: margo.RoleUser, Content: "hi"}}}
}

// drain collects a stream into text, thinking, tool calls, usage and
// the first error.
type drained struct {
	text, thinking strings.Builder
	calls          []margo.ToolCall
	usage          *margo.Usage
	err            error
}

func drain(t *testing.T, ch <-chan margo.Chunk) *drained {
	t.Helper()
	d := &drained{}
	for c := range ch {
		switch {
		case c.Err != nil:
			if d.err == nil {
				d.err = c.Err
			}
		case c.Usage != nil:
			d.usage = c.Usage
		case c.Kind == margo.ChunkText:
			d.text.WriteString(c.Text)
		case c.Kind == margo.ChunkThinking:
			d.thinking.WriteString(c.Text)
		case c.Kind == margo.ChunkToolCall:
			d.calls = append(d.calls, *c.ToolCall)
		}
	}
	return d
}

// TestSendsIdentityHeaders guards attribution on both paths. The SDK's
// own WithHTTPReferer/WithXTitle options do not reach chat requests in
// v0.9.21, so the headers are set per request; a regression here loses
// OpenRouter attribution silently.
func TestSendsIdentityHeaders(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			var referer, title atomic.Value
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				referer.Store(r.Header.Get("HTTP-Referer"))
				title.Store(r.Header.Get("X-Title"))
				if stream {
					sseReply(w, []string{`{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"ok"}}]}`})
					return
				}
				jsonReply(w, completion("ok"))
			}))
			defer srv.Close()

			c := newTestClient(srv.URL)
			if stream {
				ch, err := c.Stream(context.Background(), hi())
				if err != nil {
					t.Fatalf("Stream: %v", err)
				}
				if d := drain(t, ch); d.err != nil {
					t.Fatalf("stream error: %v", d.err)
				}
			} else if _, err := c.Complete(context.Background(), hi()); err != nil {
				t.Fatalf("Complete: %v", err)
			}
			if got, _ := referer.Load().(string); got != "https://github.com/shakfu/margo" {
				t.Errorf("HTTP-Referer = %q", got)
			}
			if got, _ := title.Load().(string); got != "margo" {
				t.Errorf("X-Title = %q", got)
			}
		})
	}
}

func TestCompleteParsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, completion("Hello via OpenRouter"))
	}))
	defer srv.Close()

	resp, err := newTestClient(srv.URL).Complete(context.Background(), hi())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Text != "Hello via OpenRouter" || resp.Model != "deepseek/deepseek-v3.2" {
		t.Errorf("resp = %+v", resp)
	}
	if resp.Usage.InputTokens != 5 || resp.Usage.OutputTokens != 4 {
		t.Errorf("Usage = %+v", resp.Usage)
	}
	if resp.Usage.Cost == nil || *resp.Usage.Cost != 0.00042 {
		t.Errorf("Usage.Cost = %v, want 0.00042", resp.Usage.Cost)
	}
}

func TestCompleteParsesToolCallsAndReasoning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, map[string]any{
			"id": "c", "object": "chat.completion", "created": 1, "model": "m",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role": "assistant", "content": nil, "reasoning": "need the weather",
					"tool_calls": []map[string]any{{
						"id": "call_1", "type": "function",
						"function": map[string]any{"name": "get_weather", "arguments": `{"city":"Paris"}`},
					}},
				},
				"finish_reason": "tool_calls",
			}},
		})
	}))
	defer srv.Close()

	resp, err := newTestClient(srv.URL).Complete(context.Background(), hi())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Thinking != "need the weather" {
		t.Errorf("Thinking = %q", resp.Thinking)
	}
	want := margo.ToolCall{ID: "call_1", Name: "get_weather", Arguments: `{"city":"Paris"}`}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0] != want {
		t.Errorf("ToolCalls = %+v, want [%+v]", resp.ToolCalls, want)
	}
}

// TestRequestMapping checks the JSON body for every margo.Request field
// the provider translates.
func TestRequestMapping(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		jsonReply(w, completion("ok"))
	}))
	defer srv.Close()

	temp, topP := 0.5, 0.9
	_, err := newTestClient(srv.URL).Complete(context.Background(), margo.Request{
		System: "be brief",
		Messages: []margo.Message{
			{Role: margo.RoleUser, Parts: []margo.Part{
				{Kind: margo.PartText, Text: "what is this?"},
				{Kind: margo.PartImage, MimeType: "image/png", Data: []byte{0x89, 0x50}},
				{Kind: margo.PartDocument, MimeType: "text/markdown", Data: []byte("# notes"), Name: "notes.md"},
			}},
			{Role: margo.RoleAssistant, ToolCalls: []margo.ToolCall{{ID: "call_1", Name: "echo", Arguments: `{"v":1}`}}},
			{Role: margo.RoleTool, ToolCallID: "call_1", Content: "1"},
		},
		MaxTokens:     256,
		Temperature:   &temp,
		TopP:          &topP,
		StopSequences: []string{"END"},
		Tools:         []margo.ToolDef{{Name: "echo", Description: "Echo.", Parameters: map[string]any{"type": "object"}}},
		ToolChoice:    "echo",
		Thinking:      &margo.Thinking{Enabled: true, BudgetTokens: 4096},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	enc, _ := json.Marshal(body)
	got := string(enc)
	for _, want := range []string{
		`"model":"deepseek/deepseek-v3.2"`,
		`{"content":"be brief","role":"system"}`,
		`{"text":"what is this?","type":"text"}`,
		`"url":"data:image/png;base64,iVA="`,
		`file name=\"notes.md\"`, // json.Marshal escapes the < as \u003c
		`"tool_calls":[{"function":{"arguments":"{\"v\":1}","name":"echo"},"id":"call_1","type":"function"}]`,
		`{"content":"1","role":"tool","tool_call_id":"call_1"}`,
		`"max_completion_tokens":256`,
		`"temperature":0.5`,
		`"top_p":0.9`,
		`"stop":["END"]`,
		`"tools":[{"function":{"description":"Echo.","name":"echo","parameters":{"type":"object"}},"type":"function"}]`,
		`"tool_choice":{"function":{"name":"echo"},"type":"function"}`,
		`"reasoning":{"effort":"medium"}`,
		`"stream":false`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("request body missing %s\nbody: %s", want, got)
		}
	}
	// An assistant turn that only calls tools must not send empty content.
	msgs := body["messages"].([]any)
	if _, ok := msgs[2].(map[string]any)["content"]; ok {
		t.Errorf("tool-call-only assistant message carries content: %v", msgs[2])
	}
}

func TestEffortFor(t *testing.T) {
	for budget, want := range map[int]string{1024: "low", 4095: "low", 4096: "medium", 16383: "medium", 16384: "high"} {
		if got := string(effortFor(budget)); got != want {
			t.Errorf("effortFor(%d) = %q, want %q", budget, got, want)
		}
	}
}

func TestStream(t *testing.T) {
	var accept atomic.Value
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept.Store(r.Header.Get("Accept"))
		_ = json.NewDecoder(r.Body).Decode(&body)
		const pre = `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":`
		sseReply(w, []string{
			pre + `{"role":"assistant","reasoning":"think"}}]}`,
			pre + `{"content":"Hel"}}]}`,
			pre + `{"content":"lo"}}]}`,
			// Two tool calls, fragments interleaved and out of index order.
			pre + `{"tool_calls":[{"index":1,"id":"b","type":"function","function":{"name":"second","arguments":"{\"y\""}}]}}]}`,
			pre + `{"tool_calls":[{"index":0,"id":"a","type":"function","function":{"name":"first","arguments":"{}"}}]}}]}`,
			pre + `{"tool_calls":[{"index":1,"function":{"arguments":":2}"}}]}}]}`,
			`{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9,"cost":0.0011}}`,
		})
	}))
	defer srv.Close()

	ch, err := newTestClient(srv.URL).Stream(context.Background(), hi())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	d := drain(t, ch)
	if d.err != nil {
		t.Fatalf("stream error: %v", d.err)
	}
	if got, _ := accept.Load().(string); got != "text/event-stream" {
		t.Errorf("Accept = %q, want text/event-stream", got)
	}
	if body["stream"] != true {
		t.Errorf("stream = %v, want true", body["stream"])
	}
	if d.text.String() != "Hello" || d.thinking.String() != "think" {
		t.Errorf("text = %q, thinking = %q", d.text.String(), d.thinking.String())
	}
	want := []margo.ToolCall{{ID: "a", Name: "first", Arguments: "{}"}, {ID: "b", Name: "second", Arguments: `{"y":2}`}}
	if len(d.calls) != 2 || d.calls[0] != want[0] || d.calls[1] != want[1] {
		t.Errorf("tool calls = %+v, want %+v", d.calls, want)
	}
	if d.usage == nil || d.usage.InputTokens != 7 || d.usage.OutputTokens != 2 {
		t.Fatalf("usage = %+v", d.usage)
	}
	if d.usage.Cost == nil || *d.usage.Cost != 0.0011 {
		t.Errorf("usage.Cost = %v, want 0.0011", d.usage.Cost)
	}
}

// TestStreamSurfacesErrors: HTTP and mid-stream failures both arrive as
// an Err chunk, never as a returned error or a silent close.
func TestStreamSurfacesErrors(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"http 401": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":401,"message":"bad key"}}`))
		},
		"in-stream error": func(w http.ResponseWriter, r *http.Request) {
			sseReply(w, []string{`{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"error":{"code":502,"message":"provider down"}}`})
		},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(h)
			defer srv.Close()
			ch, err := newTestClient(srv.URL).Stream(context.Background(), hi())
			if err != nil {
				t.Fatalf("Stream returned %v; errors belong on the channel", err)
			}
			if d := drain(t, ch); d.err == nil {
				t.Error("expected an Err chunk, got none")
			}
		})
	}
}

// TestRetriesServerErrors checks that retryPolicy is wired: one 5xx is
// retried and the call succeeds, well inside the policy's 10s bound.
func TestRetriesServerErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		jsonReply(w, completion("ok"))
	}))
	defer srv.Close()

	start := time.Now()
	resp, err := newClient("test-key", srv.URL, retryPolicy).Complete(context.Background(), hi())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Text != "ok" || calls.Load() != 2 {
		t.Errorf("text = %q after %d calls, want ok after 2", resp.Text, calls.Load())
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("retry took %v", time.Since(start))
	}
}

// TestListModels covers field mapping and pagination. With no limit
// set, the SDK keeps paging by offset until a page comes back empty.
func TestListModels(t *testing.T) {
	model := func(id string, ctx any, modalities []string, prompt, completion string) map[string]any {
		return map[string]any{
			"id": id, "canonical_slug": id, "name": id, "created": 1, "context_length": ctx,
			"architecture": map[string]any{"input_modalities": modalities, "output_modalities": []string{"text"}, "modality": "text->text"},
			"pricing":      map[string]any{"prompt": prompt, "completion": completion},
			"top_provider": map[string]any{"is_moderated": false}, "per_request_limits": nil,
			"supported_parameters": []string{}, "default_parameters": nil, "links": map[string]any{"details": ""},
		}
	}
	var (
		mu      sync.Mutex
		offsets []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset := r.URL.Query().Get("offset")
		mu.Lock()
		offsets = append(offsets, offset)
		mu.Unlock()
		data := []map[string]any{}
		switch offset {
		case "", "0":
			data = append(data, model("vision/model", 200000, []string{"text", "image"}, "0.000003", "0.000015"))
			for i := 1; i < 500; i++ {
				data = append(data, model(fmt.Sprintf("filler/%d", i), 8192, []string{"text"}, "0", "0"))
			}
		case "500":
			data = append(data, model("text/unpriced", nil, []string{"text"}, "", "bogus"))
		}
		jsonReply(w, map[string]any{"data": data, "total_count": 501, "links": map[string]any{}})
	}))
	defer srv.Close()

	models, err := newTestClient(srv.URL).ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v (offsets requested: %v)", err, offsets)
	}
	if len(models) != 501 || len(offsets) != 3 {
		t.Fatalf("got %d models over %d requests (%v), want 501 over 3", len(models), len(offsets), offsets)
	}
	v := models[0]
	if v.ID != "vision/model" || v.ContextTokens != 200000 || !v.Multimodal {
		t.Errorf("vision model = %+v", v)
	}
	if v.CostPerMTokIn == nil || *v.CostPerMTokIn != 3 || v.CostPerMTokOut == nil || *v.CostPerMTokOut != 15 {
		t.Errorf("vision prices = %v / %v, want 3 / 15", v.CostPerMTokIn, v.CostPerMTokOut)
	}
	if free := models[1]; free.CostPerMTokIn == nil || *free.CostPerMTokIn != 0 {
		t.Errorf("free model must price at 0, not unknown: %+v", free)
	}
	u := models[500]
	if u.ID != "text/unpriced" || u.ContextTokens != 0 || u.Multimodal || u.CostPerMTokIn != nil || u.CostPerMTokOut != nil {
		t.Errorf("unpriced model = %+v", u)
	}
}
