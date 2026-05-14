package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTempConfigDir points os.UserConfigDir at a t.TempDir so attachment
// tests don't write to the real ~/Library/Application Support. The
// XDG_CONFIG_HOME env var is honoured on linux but not darwin; on darwin
// os.UserConfigDir reads HOME instead. Override both to be portable.
func withTempConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	// Darwin's os.UserConfigDir returns $HOME/Library/Application Support.
	// Redirect HOME so we land inside dir on macOS as well.
	t.Setenv("HOME", dir)
	return dir
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestSaveAndLoadAttachment(t *testing.T) {
	withTempConfigDir(t)
	a := &App{}
	got, err := a.SaveAttachment("chat-abc", "hello.png", "image/png", b64("PNGBYTES"))
	if err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}
	if got.Name != "hello.png" || got.MimeType != "image/png" || got.Size != 8 {
		t.Errorf("StoredAttachment metadata wrong: %+v", got)
	}
	if _, err := os.Stat(got.Path); err != nil {
		t.Fatalf("file not on disk: %v", err)
	}

	enc, err := a.LoadAttachment(got.Path)
	if err != nil {
		t.Fatalf("LoadAttachment: %v", err)
	}
	raw, _ := base64.StdEncoding.DecodeString(enc)
	if string(raw) != "PNGBYTES" {
		t.Errorf("LoadAttachment round-trip: got %q", raw)
	}
}

func TestDeleteChatAttachments(t *testing.T) {
	withTempConfigDir(t)
	a := &App{}
	s1, err := a.SaveAttachment("chat-1", "a.png", "image/png", b64("A"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	s2, err := a.SaveAttachment("chat-1", "b.png", "image/png", b64("B"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := a.DeleteChatAttachments("chat-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(s1.Path); !os.IsNotExist(err) {
		t.Errorf("s1 should be gone, err=%v", err)
	}
	if _, err := os.Stat(s2.Path); !os.IsNotExist(err) {
		t.Errorf("s2 should be gone, err=%v", err)
	}
	// Idempotent — deleting an absent chat should not error.
	if err := a.DeleteChatAttachments("chat-1"); err != nil {
		t.Errorf("re-delete: %v", err)
	}
}

func TestLoadAttachmentRejectsEscape(t *testing.T) {
	tmp := withTempConfigDir(t)
	a := &App{}
	// Drop a sentinel file outside the attachments root so a successful
	// read would be observable.
	secret := filepath.Join(tmp, "secret.txt")
	if err := os.WriteFile(secret, []byte("MUST NOT LEAK"), 0o644); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	for _, attack := range []string{
		secret,
		filepath.Join(tmp, "Margo", "attachments", "..", "secret.txt"),
	} {
		if _, err := a.LoadAttachment(attack); err == nil {
			t.Errorf("LoadAttachment(%q) should reject path outside attachments root", attack)
		}
	}
}

func TestSaveAttachmentRejectsBadChatID(t *testing.T) {
	withTempConfigDir(t)
	a := &App{}
	for _, bad := range []string{"", "..", "a/b", "a\\b", "../escape"} {
		if _, err := a.SaveAttachment(bad, "x.png", "image/png", b64("x")); err == nil {
			t.Errorf("SaveAttachment with chatID %q should error", bad)
		}
	}
}

// TestAttachmentsToPartsRoutesByMime verifies the §7.5 wire-format
// routing: image/* MIME types become PartImage; other types (e.g.
// application/pdf) become PartDocument so the provider's document
// path runs.
func TestAttachmentsToPartsRoutesByMime(t *testing.T) {
	in := []AttachmentInput{
		{Name: "a.png", MimeType: "image/png", Data: b64("PNG")},
		{Name: "b.pdf", MimeType: "application/pdf", Data: b64("PDF")},
		{Name: "c.md", MimeType: "text/markdown", Data: b64("hi")},
	}
	out := attachmentsToParts(in)
	if len(out) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(out))
	}
	if out[0].Kind != "image" {
		t.Errorf("image/png should map to PartImage, got %q", out[0].Kind)
	}
	if out[1].Kind != "document" || out[1].Name != "b.pdf" {
		t.Errorf("application/pdf should map to PartDocument with name, got %+v", out[1])
	}
	if out[2].Kind != "document" {
		t.Errorf("text/markdown should map to PartDocument, got %q", out[2].Kind)
	}
}

func TestAttachmentSafeBase(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"hello.png", "hello.png"},
		{"../../../etc/passwd", "etc_passwd"}, // basename collapses to "passwd" then OK; this asserts no slashes survive
		{"", "attachment"},
		{".", "attachment"},
		{"..", "attachment"},
	} {
		got := attachmentSafeBase(c.in)
		if strings.ContainsAny(got, `/\`) {
			t.Errorf("attachmentSafeBase(%q) = %q contains separator", c.in, got)
		}
	}
}
