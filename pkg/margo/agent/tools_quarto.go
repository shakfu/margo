package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
)

// outputCreatedRe matches quarto's "Output created: <path>" line. The path
// is relative to cmd.Dir (the input's directory), and is the only reliable
// signal of where the rendered artifact landed without re-implementing
// quarto's output-naming rules.
var outputCreatedRe = regexp.MustCompile(`(?m)^Output created:\s*(.+?)\s*$`)

// titleRe pulls the YAML `title:` value from quarto frontmatter. Used to
// derive a meaningful filename when the model supplies `content` without
// pinning `input` — quarto's output keeps the input's basename, so naming
// the qmd `how-to-boil-an-egg.qmd` yields `how-to-boil-an-egg.pptx`.
var titleRe = regexp.MustCompile(`(?m)^title:\s*['"]?(.+?)['"]?\s*$`)

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// uniquePath returns "<dir>/<base><ext>" if it does not exist yet, or the
// first non-colliding "<dir>/<base>-<n><ext>" for n=2,3,... Used so that
// rendering the same titled document twice doesn't silently overwrite the
// first result in the shared output directory.
func uniquePath(dir, base, ext string) string {
	candidate := filepath.Join(dir, base+ext)
	if _, err := os.Stat(candidate); os.IsNotExist(err) {
		return candidate
	}
	for n := 2; n < 1000; n++ {
		c := filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, n, ext))
		if _, err := os.Stat(c); os.IsNotExist(err) {
			return c
		}
	}
	// Pathological: hand back the original; quarto will overwrite. Better
	// than failing the call entirely.
	return candidate
}

// slugFromContent returns a filesystem-friendly slug derived from the YAML
// `title:` of the supplied document, or "document" if no title is present
// or the slug would be empty.
func slugFromContent(content string) string {
	m := titleRe.FindStringSubmatch(content)
	if len(m) != 2 {
		return "document"
	}
	s := strings.ToLower(strings.TrimSpace(m[1]))
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "document"
	}
	// Cap length to keep paths reasonable on filesystems with shorter limits.
	if len(s) > 64 {
		s = strings.Trim(s[:64], "-")
	}
	return s
}

// QuartoAvailable reports whether the `quarto` binary is on PATH. The agent
// surface only registers the quarto tool when this is true; quarto is not
// embedded with margo.
func QuartoAvailable() bool {
	_, err := exec.LookPath("quarto")
	return err == nil
}

// DefaultOutputDir returns margo's stable output directory
// (`~/Documents/Margo/outputs/`), creating it if missing. Tools that
// produce user-facing artifacts should write here rather than into
// os.MkdirTemp so the user can reach them in Finder/Explorer without
// hunting through OS temp paths.
func DefaultOutputDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home dir: %w", err)
	}
	dir := filepath.Join(home, "Documents", "Margo", "outputs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create output dir: %w", err)
	}
	return dir, nil
}

// quartoFormats is the allowlist of output formats accepted by the tool.
// Restricting this prevents the model from passing arbitrary strings into
// `--to`, which quarto would otherwise forward to pandoc unchecked.
var quartoFormats = map[string]struct{}{
	"html":       {},
	"pdf":        {},
	"docx":       {},
	"pptx":       {},
	"odt":        {},
	"rtf":        {},
	"epub":       {},
	"revealjs":   {},
	"beamer":     {},
	"latex":      {},
	"markdown":   {},
	"gfm":        {},
	"asciidoc":   {},
	"typst":      {},
	"ipynb":      {},
	"jats":       {},
	"mediawiki":  {},
	"commonmark": {},
}

// quartoRenderTimeout caps a single render. Quarto execution ranges from
// sub-second (cached HTML) to many minutes (uncached PDF with computations);
// we err generous and rely on ctx cancellation as the real escape hatch.
const quartoRenderTimeout = 10 * time.Minute

type quartoArgs struct {
	Input     string `json:"input,omitempty" jsonschema:"description=Path to an existing input file (.qmd, .md, .ipynb) or quarto project directory. When 'content' is supplied this is the destination path the tool writes content to before rendering; defaults to a fresh temp directory + document.qmd if both 'input' and 'content' are omitted-or-input-is-omitted."`
	Content   string `json:"content,omitempty" jsonschema:"description=Inline Quarto document source (YAML frontmatter + markdown body). When provided the tool writes this to 'input' (or a temp path) before rendering; this is how the model creates a new document end-to-end. Required when 'input' does not yet exist."`
	To        string `json:"to,omitempty" jsonschema:"description=Output format. One of: html, pdf, docx, pptx, odt, rtf, epub, revealjs, beamer, latex, markdown, gfm, asciidoc, typst, ipynb, jats, mediawiki, commonmark. Defaults to html."`
	OutputDir string `json:"output_dir,omitempty" jsonschema:"description=Optional directory for the rendered output, relative to the input's directory. When omitted quarto writes alongside the input."`
}

// QuartoRenderTool renders a quarto document to the requested format using
// the local `quarto` CLI. Only register this tool when QuartoAvailable()
// returns true; margo does not bundle the binary.
//
// outputDir is the directory used by the create-and-render path when the
// caller (model) supplies `content` without `input`. Pass "" to fall back
// to a fresh os.MkdirTemp directory (mainly useful for tests).
func QuartoRenderTool(outputDir string) tool.InvokableTool {
	fn := func(ctx context.Context, a quartoArgs) (string, error) {
		return runQuartoRender(ctx, a, outputDir)
	}
	t, err := toolutils.InferTool(
		"quarto_render",
		"Creates and/or renders a Quarto document to the requested output format using the local `quarto` CLI. Two usage modes:\n\n1. RENDER EXISTING: pass `input` pointing to an existing `.qmd` / `.md` / `.ipynb` file (or quarto project directory). Leave `content` empty.\n2. CREATE-AND-RENDER: pass `content` with the full Quarto source (YAML frontmatter + markdown body). The tool writes it to `input` (or a temp path if `input` is omitted) and then renders. Use this mode whenever the user asks you to *make* or *generate* a document — there is no separate file-write tool, so the model must supply the source via `content`.\n\nQuarto documents use the `.qmd` extension and start with a YAML frontmatter block delimited by `---` lines that declares the title and (optionally) a `format:` map. The `format:` map keys output formats to per-format option blocks; each format takes its own keys, e.g. for HTML:\n\n---\ntitle: \"My document\"\nauthor: \"the author\"\nformat:\n  html:\n    toc: true\n    html-math-method: katex\n---\n\nfor PDF:\n\n---\ntitle: \"My document\"\nformat:\n  pdf:\n    toc: true\n    number-sections: true\n    colorlinks: true\n---\n\nMultiple formats may coexist under the same `format:` key. If the document's YAML already pins a format (or set of formats), omit the `to` argument so quarto uses what the author specified. Pass `to` only when the user explicitly asks for a different target. Supports html, pdf, docx, pptx, revealjs, beamer, latex, typst, and other pandoc targets. Returns quarto's stdout/stderr followed by an `Output file: <abs-path>` line and a `Markdown link to use verbatim in your reply: [<basename>](file://<abs-path>)` line. When telling the user where their rendered document is, paste that markdown link verbatim — do NOT replace it with a bare filename, bold text, or a relative-path link, none of which are clickable in margo's UI. Files generated via the create-and-render path land in `~/Documents/Margo/outputs/` so the user can find them in Finder/Explorer.",
		fn,
	)
	if err != nil {
		panic(err)
	}
	return t
}

func runQuartoRender(ctx context.Context, a quartoArgs, outputDir string) (string, error) {
	input := strings.TrimSpace(a.Input)
	content := a.Content
	if input == "" && strings.TrimSpace(content) == "" {
		return "", errors.New("either input (existing file) or content (inline source) is required")
	}
	format := strings.TrimSpace(a.To)
	if format == "" {
		format = "html"
	}
	if _, ok := quartoFormats[format]; !ok {
		return "", fmt.Errorf("unsupported format %q (allowed: html, pdf, docx, pptx, odt, rtf, epub, revealjs, beamer, latex, markdown, gfm, asciidoc, typst, ipynb, jats, mediawiki, commonmark)", format)
	}

	// CREATE-AND-RENDER: materialize content to disk before invoking quarto.
	if strings.TrimSpace(content) != "" {
		if input == "" {
			dir := outputDir
			if dir == "" {
				tmp, err := os.MkdirTemp("", "margo-quarto-*")
				if err != nil {
					return "", fmt.Errorf("create temp dir: %w", err)
				}
				dir = tmp
			} else if err := os.MkdirAll(dir, 0o755); err != nil {
				return "", fmt.Errorf("create output dir: %w", err)
			}
			input = uniquePath(dir, slugFromContent(content), ".qmd")
		} else if err := os.MkdirAll(filepath.Dir(input), 0o755); err != nil {
			return "", fmt.Errorf("create input parent dir: %w", err)
		}
		if err := os.WriteFile(input, []byte(content), 0o644); err != nil {
			return "", fmt.Errorf("write input: %w", err)
		}
	}

	absInput, err := filepath.Abs(input)
	if err != nil {
		return "", fmt.Errorf("resolve input path: %w", err)
	}
	if _, err := os.Stat(absInput); err != nil {
		return "", fmt.Errorf("input not found: %w", err)
	}

	cmdArgs := []string{"render", absInput, "--to", format}
	if dir := strings.TrimSpace(a.OutputDir); dir != "" {
		cmdArgs = append(cmdArgs, "--output-dir", dir)
	}

	runCtx, cancel := context.WithTimeout(ctx, quartoRenderTimeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "quarto", cmdArgs...)
	// Run from the input's directory so relative resources (images,
	// _quarto.yml, referenced data files) resolve as the user expects.
	cmd.Dir = filepath.Dir(absInput)

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	runErr := cmd.Run()
	output := strings.TrimSpace(out.String())

	if runErr != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("quarto render timed out after %s\n%s", quartoRenderTimeout, output)
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("quarto render failed: %w\n%s", runErr, output)
	}

	// Surface the absolute output path so the model can present it as a
	// usable file:// link rather than echoing quarto's relative output-line.
	outPath := ""
	if m := outputCreatedRe.FindStringSubmatch(output); len(m) == 2 {
		p := m[1]
		if !filepath.IsAbs(p) {
			p = filepath.Join(cmd.Dir, p)
		}
		outPath = p
	}

	var b strings.Builder
	if output != "" {
		b.WriteString(output)
		b.WriteString("\n\n")
	}
	if outPath != "" {
		fmt.Fprintf(&b, "Output file: %s\n", outPath)
		fmt.Fprintf(&b, "Markdown link to use verbatim in your reply: [%s](file://%s)", filepath.Base(outPath), outPath)
	} else {
		fmt.Fprintf(&b, "quarto render %s --to %s: ok", absInput, format)
	}
	return b.String(), nil
}
