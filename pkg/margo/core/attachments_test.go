package core

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestAttachmentsToPartsRoutesByMime verifies the wire-format routing
// promised by the §7.5 attachment design: image/* MIME types become
// PartImage; other types (e.g. application/pdf, text/markdown) become
// PartDocument so the provider's document path runs.
func TestAttachmentsToPartsRoutesByMime(t *testing.T) {
	in := []Attachment{
		{Name: "a.png", MimeType: "image/png", Data: []byte("PNG")},
		{Name: "b.pdf", MimeType: "application/pdf", Data: []byte("PDF")},
		{Name: "c.md", MimeType: "text/markdown", Data: []byte("hi")},
	}
	out := attachmentsToParts(in)
	if len(out) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(out))
	}
	if string(out[0].Kind) != "image" {
		t.Errorf("image/png should map to PartImage, got %q", out[0].Kind)
	}
	if string(out[1].Kind) != "document" || out[1].Name != "b.pdf" {
		t.Errorf("application/pdf should map to PartDocument with name, got %+v", out[1])
	}
	if string(out[2].Kind) != "document" {
		t.Errorf("text/markdown should map to PartDocument, got %q", out[2].Kind)
	}
}

func TestAttachmentSafeBase(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"hello.png", "hello.png"},
		{"../../../etc/passwd", "etc_passwd"},
		{"", "attachment"},
		{".", "attachment"},
		{"..", "attachment"},
	} {
		got := attachmentSafeBase(c.in)
		if strings.ContainsAny(got, `/\`) {
			t.Errorf("attachmentSafeBase(%q) = %q contains separator", c.in, got)
		}
		_ = filepath.Separator
	}
}
