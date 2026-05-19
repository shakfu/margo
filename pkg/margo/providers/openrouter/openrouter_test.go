package openrouter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/shakfu/margo/pkg/margo"
)

// newTestClient constructs an OpenRouter-shaped Client pointed at a
// test server. Preserves the production identity headers
// (HTTP-Referer, X-Title) so TestSendsIdentityHeaders below can assert
// they survive when WithBaseURL gets layered on top.
func newTestClient(serverURL string) *Client {
	return &Client{
		sdk: sdk.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(serverURL),
			option.WithHeader("HTTP-Referer", "https://github.com/shakfu/margo"),
			option.WithHeader("X-Title", "margo"),
		),
		defaultModel: "deepseek/deepseek-v3.2",
	}
}

// TestSendsIdentityHeaders is the load-bearing OpenRouter-specific
// test: the New() constructor adds HTTP-Referer and X-Title which
// OpenRouter uses for app attribution. If a future SDK upgrade reorders
// option application or drops headers on stream requests, OpenRouter
// dashboards lose attribution and free-tier rate limits get applied
// generically. Catch it here, not at the dashboard.
func TestSendsIdentityHeaders(t *testing.T) {
	var sawReferer atomic.Value
	var sawTitle atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawReferer.Store(r.Header.Get("HTTP-Referer"))
		sawTitle.Store(r.Header.Get("X-Title"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "c", "object": "chat.completion", "model": "deepseek/deepseek-v3.2",
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
		Model:    "deepseek/deepseek-v3.2",
		Messages: []margo.Message{{Role: margo.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got, _ := sawReferer.Load().(string); got != "https://github.com/shakfu/margo" {
		t.Errorf("HTTP-Referer = %q, want https://github.com/shakfu/margo", got)
	}
	if got, _ := sawTitle.Load().(string); got != "margo" {
		t.Errorf("X-Title = %q, want margo", got)
	}
}

// TestCompleteParsesResponse — OpenRouter mirrors OpenAI's chat-completion
// shape, so this is a smoke test that the SDK still decodes when pointed
// at OpenRouter's URL. The full text + usage path is covered in depth by
// the OpenAI suite (shared SDK).
func TestCompleteParsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "c", "object": "chat.completion", "model": "deepseek/deepseek-v3.2",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{"role": "assistant", "content": "Hello via OpenRouter"},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 4, "total_tokens": 9},
		})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	resp, err := c.Complete(context.Background(), margo.Request{
		Model:    "deepseek/deepseek-v3.2",
		Messages: []margo.Message{{Role: margo.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Text != "Hello via OpenRouter" {
		t.Errorf("Text = %q", resp.Text)
	}
	if resp.Model != "deepseek/deepseek-v3.2" {
		t.Errorf("Model = %q", resp.Model)
	}
	if resp.Usage.InputTokens != 5 || resp.Usage.OutputTokens != 4 {
		t.Errorf("Usage = %+v", resp.Usage)
	}
}

// TestDefaultModelUsedWhenRequestModelEmpty asserts the OpenRouter
// default model id (deepseek/deepseek-v3.2) is the fallback when the
// caller doesn't supply one. This is the only behavioral difference
// from OpenAI worth a regression net — the rest is shared SDK code.
func TestDefaultModelUsedWhenRequestModelEmpty(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "c", "object": "chat.completion", "model": "deepseek/deepseek-v3.2",
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
		Messages: []margo.Message{{Role: margo.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got, _ := captured["model"].(string); got != "deepseek/deepseek-v3.2" {
		t.Errorf("request model = %q, want deepseek/deepseek-v3.2 fallback", got)
	}
}
