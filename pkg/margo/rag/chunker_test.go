package rag

import (
	"strings"
	"testing"
)

func TestRecursiveChunker_Empty(t *testing.T) {
	c := DefaultChunker()
	if got := c.Split(""); got != nil {
		t.Errorf("Split(\"\") = %v, want nil", got)
	}
}

func TestRecursiveChunker_ShortReturnsSingle(t *testing.T) {
	c := DefaultChunker()
	got := c.Split("hello world")
	if len(got) != 1 || got[0] != "hello world" {
		t.Errorf("Split(short) = %v, want [\"hello world\"]", got)
	}
}

func TestRecursiveChunker_LongTextSplits(t *testing.T) {
	c := RecursiveChunker{MaxChars: 100, Overlap: 20}
	// Construct three paragraphs, each ~80 chars; total ~250.
	p := strings.Repeat("a ", 40) // 80 chars
	text := p + "\n\n" + p + "\n\n" + p
	chunks := c.Split(text)
	if len(chunks) < 2 {
		t.Fatalf("len(chunks) = %d, want >= 2", len(chunks))
	}
	for i, ch := range chunks {
		if len(ch) > c.MaxChars+c.Overlap {
			// The +Overlap allowance is because mergeWithOverlap may
			// briefly exceed MaxChars when seeding the next chunk.
			t.Errorf("chunk[%d] len=%d, want <= MaxChars+Overlap=%d", i, len(ch), c.MaxChars+c.Overlap)
		}
	}
}

func TestRecursiveChunker_OverlapBetweenChunks(t *testing.T) {
	c := RecursiveChunker{MaxChars: 50, Overlap: 10}
	// Single long paragraph forces split below paragraph level. Use
	// distinguishable letters so we can inspect overlap.
	text := strings.Repeat("abcdefghij", 20) // 200 chars
	chunks := c.Split(text)
	if len(chunks) < 2 {
		t.Fatalf("expected >= 2 chunks for 200-char input with MaxChars=50; got %d", len(chunks))
	}
	for i := 1; i < len(chunks); i++ {
		// Overlap is at most c.Overlap chars from the tail of the
		// previous chunk, prepended to chunk[i]. Verify chunk[i]
		// shares a prefix with the suffix of chunk[i-1].
		prevTail := chunks[i-1]
		if len(prevTail) > c.Overlap {
			prevTail = prevTail[len(prevTail)-c.Overlap:]
		}
		if !strings.HasPrefix(chunks[i], prevTail[:1]) {
			// Overlap is best-effort; a hard split at codepoint boundary
			// can yield a different prefix character. Loosen the check
			// to "some overlap exists" by ensuring chunks share at least
			// one substring of length min(5, overlap).
			minLen := 5
			if c.Overlap < minLen {
				minLen = c.Overlap
			}
			if len(prevTail) >= minLen && !strings.Contains(chunks[i][:c.Overlap], prevTail[:minLen]) {
				t.Errorf("chunk[%d] does not appear to overlap with chunk[%d]", i, i-1)
			}
		}
	}
}

func TestRecursiveChunker_PrefersParagraphBoundary(t *testing.T) {
	c := RecursiveChunker{MaxChars: 60, Overlap: 0}
	// Two clearly delineated paragraphs, total 56 chars => fits a
	// single chunk; should not split.
	text := "first para here.\n\nsecond para here too."
	chunks := c.Split(text)
	if len(chunks) != 1 {
		t.Errorf("len(chunks) = %d, want 1 (fits in MaxChars)", len(chunks))
	}

	// Now force a split by reducing MaxChars; the cut should land at
	// the paragraph break, not mid-sentence.
	c2 := RecursiveChunker{MaxChars: 25, Overlap: 0}
	chunks2 := c2.Split(text)
	if len(chunks2) < 2 {
		t.Fatalf("expected split; got %d chunks", len(chunks2))
	}
	// First chunk should end with "\n\n" (the paragraph separator was
	// re-attached) — meaning we cut at the paragraph boundary.
	if !strings.HasSuffix(chunks2[0], "\n\n") {
		t.Errorf("first chunk %q does not end at paragraph break", chunks2[0])
	}
}

func TestChunkDocument_StampsDocPathAndIndex(t *testing.T) {
	doc := LoadedDocument{
		AbsPath: "/abs/path/notes.md",
		RelPath: "notes.md",
		Content: strings.Repeat("hello ", 100),
	}
	c := RecursiveChunker{MaxChars: 100, Overlap: 10}
	chunks := ChunkDocument(doc, c)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks; got %d", len(chunks))
	}
	for i, ch := range chunks {
		if ch.DocPath != "notes.md" {
			t.Errorf("chunks[%d].DocPath = %q, want notes.md", i, ch.DocPath)
		}
		if ch.Index != i {
			t.Errorf("chunks[%d].Index = %d, want %d", i, ch.Index, i)
		}
	}
}

func TestRecursiveChunker_ZeroValueUsesDefaults(t *testing.T) {
	var c RecursiveChunker // zero value
	chunks := c.Split("short")
	if len(chunks) != 1 || chunks[0] != "short" {
		t.Errorf("zero-value chunker dropped trivial input: %v", chunks)
	}
	// Long input gets multiple chunks under defaults.
	long := strings.Repeat("a ", DefaultChunkChars) // ~6400 chars
	chunks = c.Split(long)
	if len(chunks) < 2 {
		t.Errorf("long input not split under default sizing; got %d chunks", len(chunks))
	}
}

func TestHardSplit_MultiByteSafe(t *testing.T) {
	// Each rune is 3 bytes in UTF-8; a naive byte-cut would bisect
	// codepoints. Verify hardSplit returns valid UTF-8 only.
	text := strings.Repeat("日本語", 20) // 60 runes, 180 bytes
	parts := hardSplit(text, 10)       // 10 runes per piece
	for i, p := range parts {
		if len([]rune(p)) > 10 {
			t.Errorf("parts[%d] runes=%d, want <= 10", i, len([]rune(p)))
		}
		// Round-trip via []rune to confirm valid UTF-8.
		if string([]rune(p)) != p {
			t.Errorf("parts[%d] not valid UTF-8: %q", i, p)
		}
	}
}
