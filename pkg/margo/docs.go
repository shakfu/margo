package margo

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/ledongthuc/pdf"
)

// MaxExtractedDocChars caps how much extracted text a single document
// attachment can contribute to the prompt. PDFs of any size could
// otherwise blow through a model's context window via the
// text-extraction fallback; cap at ~25k tokens of text (chars/4
// heuristic) so attaching a 500-page book at least surfaces an error
// rather than silently producing a 200k-token request.
const MaxExtractedDocChars = 100_000

// ExtractTextFromDocument returns a plain-text rendering of a document
// Part suitable for inlining into the prompt of a provider that doesn't
// accept the native document format. Today only application/pdf is
// supported; plain text MIME types are returned as-is. Returns an error
// for anything else so the caller can produce a clear "unsupported"
// message rather than silently dropping the attachment.
//
// Output is wrapped in `<file name="...">` ... `</file>` so the model
// can attribute the contents to a specific attachment when the same
// turn carries multiple documents.
func ExtractTextFromDocument(p Part, name string) (string, error) {
	if len(p.Data) == 0 {
		return "", fmt.Errorf("document %q is empty", name)
	}
	var body string
	switch {
	case p.MimeType == "application/pdf":
		txt, err := extractPDFText(p.Data)
		if err != nil {
			return "", fmt.Errorf("extract pdf %q: %w", name, err)
		}
		body = txt
	case strings.HasPrefix(p.MimeType, "text/"):
		body = string(p.Data)
	default:
		return "", fmt.Errorf("unsupported document mime type %q for %q", p.MimeType, name)
	}
	body = strings.TrimSpace(body)
	if len(body) > MaxExtractedDocChars {
		body = body[:MaxExtractedDocChars] + "\n\n[truncated]"
	}
	if name == "" {
		name = "attachment"
	}
	return fmt.Sprintf("<file name=%q>\n%s\n</file>", name, body), nil
}

func extractPDFText(raw []byte) (string, error) {
	r, err := pdf.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return "", err
	}
	rdr, err := r.GetPlainText()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if _, err := io.Copy(&b, rdr); err != nil {
		return "", err
	}
	return b.String(), nil
}
