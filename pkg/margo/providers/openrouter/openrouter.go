// Package openrouter implements margo.Client against OpenRouter, which
// serves the OpenAI Chat Completions wire format for several hundred
// models from other vendors.
//
// The wire-format work lives in providers/openaicompat, which the OpenAI
// provider shares. This package is the OpenRouter-specific
// configuration: base URL, model default, and the identity headers
// OpenRouter uses for app attribution.
package openrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shakfu/margo/pkg/margo"
	"github.com/shakfu/margo/pkg/margo/providers/openaicompat"
)

// baseURL is OpenRouter's OpenAI-compatible endpoint.
const baseURL = "https://openrouter.ai/api/v1"

// defaultModel is used when a Request leaves Model empty.
const defaultModel = "deepseek/deepseek-v3.2"

// identityHeaders are what OpenRouter attributes traffic with. Without
// them requests are attributed generically and free-tier rate limits
// apply to the pool rather than to margo.
var identityHeaders = map[string]string{
	"HTTP-Referer": "https://github.com/shakfu/margo",
	"X-Title":      "margo",
}

// Client is an OpenRouter-configured openaicompat.Client. Embedded
// rather than aliased so ListModels below can replace the inherited
// OpenAI-shaped one — OpenRouter's catalog endpoint returns strictly
// more, and decoding it needs its own type.
type Client struct {
	*openaicompat.Client
}

// New returns a client for the OpenRouter API.
func New(apiKey string) *Client {
	return &Client{openaicompat.New(openaicompat.Options{
		Name:         "openrouter",
		APIKey:       apiKey,
		BaseURL:      baseURL,
		DefaultModel: defaultModel,
		Headers:      identityHeaders,
	})}
}

// modelsResponse is the shape of GET /api/v1/models. OpenRouter serves
// the OpenAI wire format for completions but its model catalog is its
// own: richer than OpenAI's (context length, modalities, per-token
// prices) and not something the openai-go SDK's Models.List can decode.
type modelsResponse struct {
	Data []struct {
		ID            string `json:"id"`
		ContextLength int    `json:"context_length"`
		Architecture  struct {
			InputModalities []string `json:"input_modalities"`
		} `json:"architecture"`
		Pricing struct {
			// Prices are USD per token, as decimal strings.
			Prompt     string `json:"prompt"`
			Completion string `json:"completion"`
		} `json:"pricing"`
	} `json:"data"`
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

// ListModels fetches OpenRouter's catalog. The endpoint is public and
// unauthenticated, and reports everything margo's catalog declares —
// context window, image support, and both token prices — so an
// OpenRouter model needs no entry in the embedded models.json.
func (c *Client) ListModels(ctx context.Context) ([]margo.Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("openrouter: build models request: %w", err)
	}
	for k, v := range identityHeaders {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter: list models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openrouter: list models: http %d", resp.StatusCode)
	}

	var body modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("openrouter: decode models: %w", err)
	}

	out := make([]margo.Model, 0, len(body.Data))
	for _, m := range body.Data {
		if m.ID == "" {
			continue
		}
		multimodal := false
		for _, mod := range m.Architecture.InputModalities {
			if mod == "image" {
				multimodal = true
				break
			}
		}
		out = append(out, margo.Model{
			ID:             m.ID,
			ContextTokens:  m.ContextLength,
			Multimodal:     multimodal,
			CostPerMTokIn:  perMTok(m.Pricing.Prompt),
			CostPerMTokOut: perMTok(m.Pricing.Completion),
			PricedAt:       time.Now().UTC().Format("2006-01-02"),
		})
	}
	return out, nil
}
