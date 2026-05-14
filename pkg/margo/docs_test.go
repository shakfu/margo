package margo

import (
	"os"
	"strings"
	"testing"
)

// TestExtractTextFromPDF is the end-to-end happy path for the PDF
// fallback used by OpenAI / OpenRouter. Reads a real PDF fixture and
// asserts the extracted text contains the known content. Whitespace
// and word ordering are library-dependent (ledongthuc/pdf), so we
// substring-match rather than diff exact bytes.
func TestExtractTextFromPDF(t *testing.T) {
	data, err := os.ReadFile("testdata/hello.pdf")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	out, err := ExtractTextFromDocument(
		Part{Kind: PartDocument, MimeType: "application/pdf", Data: data},
		"hello.pdf",
	)
	if err != nil {
		t.Fatalf("ExtractTextFromDocument: %v", err)
	}
	if !strings.Contains(out, `<file name="hello.pdf">`) {
		t.Errorf("expected wrapper with filename, got %q", out)
	}
	// The fixture text. Lower-case both sides so we don't care whether
	// the PDF extractor preserves capitalisation across word boundaries.
	if !strings.Contains(strings.ToLower(out), "hello") || !strings.Contains(strings.ToLower(out), "test") {
		t.Errorf("expected fixture text in extracted output, got %q", out)
	}
}

func TestExtractTextFromDocumentText(t *testing.T) {
	p := Part{Kind: PartDocument, MimeType: "text/markdown", Data: []byte("# title\n\nbody")}
	out, err := ExtractTextFromDocument(p, "notes.md")
	if err != nil {
		t.Fatalf("ExtractTextFromDocument: %v", err)
	}
	if !strings.Contains(out, `<file name="notes.md">`) || !strings.Contains(out, "body") {
		t.Errorf("output missing wrapper or body: %q", out)
	}
}

func TestExtractTextFromDocumentUnsupported(t *testing.T) {
	p := Part{Kind: PartDocument, MimeType: "application/octet-stream", Data: []byte{0x00, 0x01}}
	if _, err := ExtractTextFromDocument(p, "blob.bin"); err == nil {
		t.Fatalf("expected error for unsupported mime type")
	}
}

func TestExtractTextFromDocumentEmpty(t *testing.T) {
	p := Part{Kind: PartDocument, MimeType: "application/pdf"}
	if _, err := ExtractTextFromDocument(p, "x.pdf"); err == nil {
		t.Fatalf("expected error for empty data")
	}
}

func TestExtractTextFromDocumentTruncates(t *testing.T) {
	body := strings.Repeat("x", MaxExtractedDocChars+1000)
	p := Part{Kind: PartDocument, MimeType: "text/plain", Data: []byte(body)}
	out, err := ExtractTextFromDocument(p, "big.txt")
	if err != nil {
		t.Fatalf("ExtractTextFromDocument: %v", err)
	}
	if !strings.Contains(out, "[truncated]") {
		t.Errorf("expected truncation marker in oversized output")
	}
}

func TestExtractTextFromPDFInvalid(t *testing.T) {
	// Not a real PDF; just bytes labelled as one. Should surface a clear
	// error rather than panic in the underlying reader.
	p := Part{Kind: PartDocument, MimeType: "application/pdf", Data: []byte("not a pdf")}
	if _, err := ExtractTextFromDocument(p, "fake.pdf"); err == nil {
		t.Fatalf("expected error for malformed PDF input")
	}
}
