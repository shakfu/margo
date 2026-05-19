package core

import (
	cryptorand "crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AttachmentStore manages on-disk storage of chat attachments. Bytes in,
// bytes out — base64 (or any other transport encoding) is a frontend
// concern, kept out of the store so a TUI or HTTP server can use the
// same code without an unnecessary encode/decode round-trip.
type AttachmentStore struct {
	root string // override; empty = derive from os.UserConfigDir
}

// NewAttachmentStore returns a store rooted at <UserConfigDir>/Margo/attachments.
// Pass a non-empty rootOverride to redirect storage (used in tests).
func NewAttachmentStore(rootOverride string) *AttachmentStore {
	return &AttachmentStore{root: rootOverride}
}

// Root returns the absolute on-disk root for attachments, creating nothing.
func (s *AttachmentStore) Root() (string, error) {
	if s.root != "" {
		return s.root, nil
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("user config dir: %w", err)
	}
	return filepath.Join(cfg, "Margo", "attachments"), nil
}

// Save writes the raw bytes under <root>/<chatID>/<unique-name>. Filenames
// are de-collided with a timestamp + random suffix; the original name is
// preserved on the returned record as a UX label.
func (s *AttachmentStore) Save(chatID, name, mimeType string, data []byte) (StoredAttachment, error) {
	if err := validateChatID(chatID); err != nil {
		return StoredAttachment{}, err
	}
	if len(data) == 0 {
		return StoredAttachment{}, fmt.Errorf("empty attachment")
	}
	root, err := s.Root()
	if err != nil {
		return StoredAttachment{}, err
	}
	dir := filepath.Join(root, chatID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return StoredAttachment{}, fmt.Errorf("mkdir: %w", err)
	}
	safe := attachmentSafeBase(name)
	stamp := time.Now().UnixNano()
	rndBuf := make([]byte, 6)
	if _, err := io.ReadFull(cryptorand.Reader, rndBuf); err != nil {
		return StoredAttachment{}, fmt.Errorf("rand: %w", err)
	}
	filename := fmt.Sprintf("%d-%x-%s", stamp, rndBuf, safe)
	abs := filepath.Join(dir, filename)
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return StoredAttachment{}, fmt.Errorf("write: %w", err)
	}
	return StoredAttachment{
		Path:     abs,
		Name:     name,
		MimeType: mimeType,
		Size:     int64(len(data)),
	}, nil
}

// Load reads the bytes back from disk. The path is validated to live under
// the store's root, so this method cannot be turned into an arbitrary file
// reader by a malicious caller.
func (s *AttachmentStore) Load(path string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	root, err := s.Root()
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("abs: %w", err)
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(rel, "..") || strings.Contains(rel, "..") {
		return nil, fmt.Errorf("path outside attachments root")
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	return raw, nil
}

// DeleteChat removes every blob stored for the given chat. Idempotent:
// a missing directory is not an error.
func (s *AttachmentStore) DeleteChat(chatID string) error {
	if err := validateChatID(chatID); err != nil {
		return err
	}
	root, err := s.Root()
	if err != nil {
		return err
	}
	dir := filepath.Join(root, chatID)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("delete chat attachments: %w", err)
	}
	return nil
}

// validateChatID rejects ids that would let a caller write outside the
// per-chat subtree. Chat ids in the frontend are crypto.randomUUID(); the
// allow-list keeps that intent without trusting the caller.
func validateChatID(id string) error {
	if id == "" {
		return fmt.Errorf("chat id is required")
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return fmt.Errorf("invalid chat id %q", id)
	}
	return nil
}

// attachmentSafeBase strips path separators and other risky characters so
// a user-supplied filename can land on disk under a known directory.
func attachmentSafeBase(name string) string {
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "attachment"
	}
	if len(name) > 80 {
		ext := filepath.Ext(name)
		if len(ext) > 16 {
			ext = ""
		}
		name = name[:80-len(ext)] + ext
	}
	return name
}
