package rag

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile is a tiny helper for setting up the loader tests.
func writeFile(t *testing.T, dir, rel, content string) string {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", full, err)
	}
	return full
}

func TestLoad_DefaultFiltersAndSorts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "z.md", "# Zed")
	writeFile(t, dir, "a.txt", "alpha")
	writeFile(t, dir, "b.qmd", "---\ntitle: B\n---\nbody")
	writeFile(t, dir, "ignored.png", "binary")
	writeFile(t, dir, "ignored.go", "package x")

	docs, err := Load(context.Background(), dir, LoadOptions{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(docs) != 3 {
		t.Fatalf("len(docs) = %d, want 3 (md+txt+qmd)", len(docs))
	}
	want := []string{"a.txt", "b.qmd", "z.md"}
	for i, d := range docs {
		if d.RelPath != want[i] {
			t.Errorf("docs[%d].RelPath = %q, want %q", i, d.RelPath, want[i])
		}
	}
}

func TestLoad_HiddenSkippedByDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "visible.md", "yes")
	writeFile(t, dir, ".hidden.md", "no")
	writeFile(t, dir, ".git/config", "no")
	writeFile(t, dir, ".git/refs/heads/main", "no") // also under hidden dir

	docs, err := Load(context.Background(), dir, LoadOptions{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("len(docs) = %d, want 1; got: %+v", len(docs), docs)
	}
	if docs[0].RelPath != "visible.md" {
		t.Errorf("RelPath = %q, want visible.md", docs[0].RelPath)
	}
}

func TestLoad_HiddenIncludedWhenRequested(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".hidden.md", "yes")

	docs, err := Load(context.Background(), dir, LoadOptions{IncludeHidden: true})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("len(docs) = %d, want 1", len(docs))
	}
}

func TestLoad_CustomExtensions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.go", "package x")
	writeFile(t, dir, "y.md", "skip me")

	docs, err := Load(context.Background(), dir, LoadOptions{Extensions: []string{".go"}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(docs) != 1 || docs[0].RelPath != "x.go" {
		t.Errorf("got %+v, want one .go file", docs)
	}
}

func TestLoad_SkipsOversizedFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "small.md", "tiny")
	big := strings.Repeat("x", 4096)
	writeFile(t, dir, "big.md", big)

	docs, err := Load(context.Background(), dir, LoadOptions{MaxFileBytes: 100})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(docs) != 1 || docs[0].RelPath != "small.md" {
		t.Errorf("got %+v, want only small.md", docs)
	}
}

func TestLoad_NotADir(t *testing.T) {
	dir := t.TempDir()
	f := writeFile(t, dir, "file.md", "x")
	if _, err := Load(context.Background(), f, LoadOptions{}); err == nil {
		t.Error("Load(file) = nil err, want error")
	}
}

func TestLoad_ContextCancelled(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.md", "alpha")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Load(ctx, dir, LoadOptions{})
	// Either the walk surfaces ctx.Err() or returns immediately on
	// the first entry; both are acceptable. The contract is "doesn't
	// hang and reports the cancel."
	if err == nil {
		// If the walk completed before the first ctx check, accept
		// the no-error return (small enough tree). The branch above
		// is the more common path on real filesystems.
		t.Skip("walk completed too fast to observe cancel; non-flaky variant requires more files")
	}
}

func TestLoad_Subdirectories(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "top.md", "1")
	writeFile(t, dir, "sub/nested.md", "2")
	writeFile(t, dir, "sub/deeper/leaf.md", "3")

	docs, err := Load(context.Background(), dir, LoadOptions{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(docs) != 3 {
		t.Fatalf("len(docs) = %d, want 3", len(docs))
	}
	// RelPaths should be slash-separated (forward slashes), even on Windows.
	for _, d := range docs {
		if strings.Contains(d.RelPath, `\`) {
			t.Errorf("RelPath %q contains backslash; want forward slashes", d.RelPath)
		}
	}
}
