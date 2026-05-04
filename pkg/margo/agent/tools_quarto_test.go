package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQuartoRenderArgValidation(t *testing.T) {
	// These checks run before any exec call, so they're independent of
	// whether quarto is installed on the host.
	cases := []struct {
		name    string
		args    quartoArgs
		errLike string
	}{
		{"empty input and content", quartoArgs{}, "input (existing file) or content"},
		{"unsupported format", quartoArgs{Input: "x.qmd", To: "rot13"}, "unsupported format"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runQuartoRender(context.Background(), tc.args, "")
			if err == nil || !strings.Contains(err.Error(), tc.errLike) {
				t.Fatalf("got err=%v, want substring %q", err, tc.errLike)
			}
		})
	}
}

// TestQuartoRenderHTML actually invokes the quarto CLI to render a tiny
// markdown file to HTML. Skipped when quarto is not on PATH, so the test
// suite stays portable.
func TestQuartoRenderHTML(t *testing.T) {
	if !QuartoAvailable() {
		t.Skip("quarto not on PATH")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "doc.qmd")
	if err := os.WriteFile(src, []byte("---\ntitle: t\n---\n\nhello\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	out, err := runQuartoRender(context.Background(), quartoArgs{Input: src, To: "html"}, "")
	if err != nil {
		t.Fatalf("render: %v\n%s", err, out)
	}

	if _, err := os.Stat(filepath.Join(dir, "doc.html")); err != nil {
		t.Fatalf("expected doc.html in %s, stat err: %v", dir, err)
	}
}

func TestSlugFromContent(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"---\ntitle: \"How to Boil an Egg\"\n---\n", "how-to-boil-an-egg"},
		{"---\ntitle: 'Mixed/Punct & Whitespace!'\n---\n", "mixed-punct-whitespace"},
		{"---\ntitle: bare value\n---\n", "bare-value"},
		{"no frontmatter at all", "document"},
		{"---\ntitle: \"   \"\n---", "document"},
	}
	for _, tc := range cases {
		if got := slugFromContent(tc.in); got != tc.want {
			t.Errorf("slugFromContent(%q): got %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestQuartoRenderCreateAndRender exercises the create-and-render path: the
// model supplies inline `content` and the tool writes it to `input` before
// invoking quarto. Skipped when quarto is unavailable.
func TestQuartoRenderCreateAndRender(t *testing.T) {
	if !QuartoAvailable() {
		t.Skip("quarto not on PATH")
	}

	dir := t.TempDir()
	dst := filepath.Join(dir, "presentation.qmd")
	src := "---\ntitle: \"t\"\nformat: html\n---\n\nbody\n"

	if _, err := runQuartoRender(context.Background(), quartoArgs{
		Input:   dst,
		Content: src,
		To:      "html",
	}, ""); err != nil {
		t.Fatalf("render: %v", err)
	}

	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("expected the qmd to be written at %s: %v", dst, err)
	}
	expected := filepath.Join(dir, "presentation.html")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("expected presentation.html: %v", err)
	}

	out, err := runQuartoRender(context.Background(), quartoArgs{Input: dst, To: "html"}, "")
	if err != nil {
		t.Fatalf("re-render: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Output file: "+expected) {
		t.Errorf("missing absolute Output file line in result; got:\n%s", out)
	}
	wantLink := "[presentation.html](file://" + expected + ")"
	if !strings.Contains(out, wantLink) {
		t.Errorf("missing markdown link %q in result; got:\n%s", wantLink, out)
	}
}

// TestQuartoRenderTitleSlug verifies that supplying `content` with a YAML
// title and no `input` produces a filename derived from the title rather
// than a generic "document.qmd".
func TestQuartoRenderTitleSlug(t *testing.T) {
	if !QuartoAvailable() {
		t.Skip("quarto not on PATH")
	}

	dir := t.TempDir()
	src := "---\ntitle: \"How to Boil an Egg\"\nformat: html\n---\n\nbody\n"
	out, err := runQuartoRender(context.Background(), quartoArgs{Content: src, To: "html"}, dir)
	if err != nil {
		t.Fatalf("render: %v\n%s", err, out)
	}
	expected := filepath.Join(dir, "how-to-boil-an-egg.html")
	if !strings.Contains(out, expected) {
		t.Errorf("expected output to be in configured outputDir %s; got:\n%s", dir, out)
	}
	wantLink := "[how-to-boil-an-egg.html](file://" + expected + ")"
	if !strings.Contains(out, wantLink) {
		t.Errorf("missing markdown link %q in result; got:\n%s", wantLink, out)
	}

	// Second render of the same title — confirm uniquePath kicks in so we
	// don't silently overwrite the first artifact.
	out2, err := runQuartoRender(context.Background(), quartoArgs{Content: src, To: "html"}, dir)
	if err != nil {
		t.Fatalf("second render: %v\n%s", err, out2)
	}
	if !strings.Contains(out2, "how-to-boil-an-egg-2") {
		t.Errorf("expected -2 suffix on collision; got:\n%s", out2)
	}
}
