package agent

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/shakfu/margo/pkg/margo"
)

// TestAdapterFinalUserAttachments verifies that the adapter stamps the
// configured Parts onto the LAST user-role margo.Message, preserving the
// existing Content as a leading text part. Earlier user turns and
// non-user turns are left alone.
func TestAdapterFinalUserAttachments(t *testing.T) {
	parts := []margo.Part{
		{Kind: margo.PartImage, MimeType: "image/png", Data: []byte{1, 2, 3}},
	}
	a := NewAdapter(nopClient{}, margo.Request{}).WithFinalUserAttachments(parts)

	input := []*schema.Message{
		{Role: schema.User, Content: "first turn"},
		{Role: schema.Assistant, Content: "ok"},
		{Role: schema.User, Content: "second turn"},
	}
	req := a.request(input)

	if got := len(req.Messages); got != 3 {
		t.Fatalf("got %d messages, want 3", got)
	}
	// First user turn — no parts.
	if len(req.Messages[0].Parts) != 0 {
		t.Errorf("first user turn unexpectedly has parts: %#v", req.Messages[0].Parts)
	}
	// Last user turn — text + image.
	last := req.Messages[2]
	if len(last.Parts) != 2 {
		t.Fatalf("last user turn parts: got %d, want 2", len(last.Parts))
	}
	if last.Parts[0].Kind != margo.PartText || last.Parts[0].Text != "second turn" {
		t.Errorf("first part should be the existing Content as text, got %#v", last.Parts[0])
	}
	if last.Parts[1].Kind != margo.PartImage || last.Parts[1].MimeType != "image/png" {
		t.Errorf("second part should be the supplied image, got %#v", last.Parts[1])
	}
}

func TestAdapterNoAttachments(t *testing.T) {
	a := NewAdapter(nopClient{}, margo.Request{})
	req := a.request([]*schema.Message{{Role: schema.User, Content: "hi"}})
	if len(req.Messages) != 1 || len(req.Messages[0].Parts) != 0 {
		t.Errorf("expected single text-only message, got %#v", req.Messages)
	}
}

// nopClient is a minimal margo.Client used by tests that exercise the
// adapter's request-construction logic without making real network calls.
type nopClient struct{}

func (nopClient) Name() string                                                          { return "nop" }
func (nopClient) Complete(context.Context, margo.Request) (margo.Response, error)       { return margo.Response{}, nil }
func (nopClient) Stream(context.Context, margo.Request) (<-chan margo.Chunk, error)     { return nil, nil }
