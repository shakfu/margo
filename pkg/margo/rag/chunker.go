package rag

import "strings"

// Default chunker sizing. Token estimation is the same chars/4 heuristic
// used by the budget rewriter (pkg/margo/agent/budget.go), kept coarse on
// purpose: no tokenizer dependency, and cheap to compute on indexing
// hot paths.
//
// 800 tokens × 4 chars/token = 3200 chars target chunk size.
// 100 tokens × 4 = 400 chars overlap. These are starting points
// validated by LangChain's defaults; tune per-corpus later if
// retrieval quality demands it.
const (
	DefaultChunkChars   = 3200
	DefaultChunkOverlap = 400
)

// Chunk is one piece of a LoadedDocument, ready to be embedded.
//
// DocPath is the source document's RelPath (slash-separated, stable).
// Index is 0-based within the document, monotonically increasing.
// The combination DocPath + Index is unique within a corpus and is
// the natural id format for VectorStore upserts.
type Chunk struct {
	DocPath string
	Index   int
	Content string
}

// RecursiveChunker splits text by recursively descending through
// natural break points (paragraph → line → sentence → word →
// character) until each piece fits MaxChars. Overlap chars from the
// tail of each chunk are prepended to the next so retrieval doesn't
// miss content straddling a boundary.
//
// Zero value behaves as DefaultChunker(): MaxChars=3200, Overlap=400.
type RecursiveChunker struct {
	MaxChars int
	Overlap  int
}

// DefaultChunker returns a chunker tuned per the package constants.
func DefaultChunker() RecursiveChunker {
	return RecursiveChunker{MaxChars: DefaultChunkChars, Overlap: DefaultChunkOverlap}
}

// chunkSeparators are tried in order. Each level represents a
// progressively finer split: prefer cutting at paragraph breaks; fall
// through to line, sentence, word, character if a level produces
// pieces still larger than MaxChars.
//
// The empty string is the terminator: treat the input as a stream of
// runes and cut by length.
var chunkSeparators = []string{"\n\n", "\n", ". ", " ", ""}

// Split returns text broken into chunks no larger than MaxChars,
// joined back into "windows" with `Overlap` chars of trailing context
// duplicated into the next chunk's head. Returns a slice with at
// least one entry for any non-empty input. Empty input returns nil.
func (c RecursiveChunker) Split(text string) []string {
	max := c.MaxChars
	if max <= 0 {
		max = DefaultChunkChars
	}
	overlap := c.Overlap
	if overlap < 0 || overlap >= max {
		overlap = DefaultChunkOverlap
		if overlap >= max {
			overlap = max / 8
		}
	}
	if text == "" {
		return nil
	}
	if len(text) <= max {
		return []string{text}
	}
	pieces := splitRecursive(text, max, chunkSeparators)
	return mergeWithOverlap(pieces, max, overlap)
}

// splitRecursive walks the separator hierarchy. At each level: split
// the input on that separator; for any resulting piece still larger
// than max, recurse with the next separator.
func splitRecursive(text string, max int, seps []string) []string {
	if len(text) <= max {
		return []string{text}
	}
	if len(seps) == 0 {
		// Last resort: hard-cut by length.
		return hardSplit(text, max)
	}
	sep := seps[0]
	if sep == "" {
		return hardSplit(text, max)
	}
	parts := strings.Split(text, sep)
	out := make([]string, 0, len(parts))
	for i, p := range parts {
		// Re-attach the separator to all but the last fragment so
		// content joined back together reproduces the original up
		// to overlap windows. (sep is preserved at the *end* of
		// each fragment except the final one.)
		if i < len(parts)-1 {
			p = p + sep
		}
		if len(p) <= max {
			if p != "" {
				out = append(out, p)
			}
			continue
		}
		// Still too big: recurse with the next-finer separator.
		out = append(out, splitRecursive(p, max, seps[1:])...)
	}
	return out
}

// hardSplit cuts text into max-sized slices irrespective of structure.
// Operates on runes (multi-byte safe) to avoid splitting mid-codepoint.
func hardSplit(text string, max int) []string {
	if text == "" {
		return nil
	}
	r := []rune(text)
	if len(r) <= max {
		return []string{string(r)}
	}
	var out []string
	for i := 0; i < len(r); i += max {
		end := i + max
		if end > len(r) {
			end = len(r)
		}
		out = append(out, string(r[i:end]))
	}
	return out
}

// mergeWithOverlap accretes pieces into chunks <= max chars, prefixing
// each (after the first) with up to `overlap` chars from the tail of
// the previous chunk. Operates on byte length; assumes pieces are
// short enough that small overlap windows won't bisect a multi-byte
// codepoint (acceptable approximation given char-count is itself a
// token-count heuristic).
func mergeWithOverlap(pieces []string, max, overlap int) []string {
	if len(pieces) == 0 {
		return nil
	}
	var chunks []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		chunks = append(chunks, cur.String())
		// Seed next chunk with overlap from the just-flushed one.
		tail := lastN(cur.String(), overlap)
		cur.Reset()
		cur.WriteString(tail)
	}
	for _, p := range pieces {
		if cur.Len()+len(p) > max && cur.Len() > 0 {
			flush()
		}
		cur.WriteString(p)
	}
	if cur.Len() > 0 {
		chunks = append(chunks, cur.String())
	}
	return chunks
}

// lastN returns the last n chars of s. Char-counted on bytes; safe
// for ASCII-heavy corpora (the dominant case for source code,
// markdown, and plain text). Caller-controlled; if it ever gets
// promoted to a public API, switch to runes.
func lastN(s string, n int) string {
	if n <= 0 || len(s) == 0 {
		return ""
	}
	if n >= len(s) {
		return s
	}
	return s[len(s)-n:]
}

// ChunkDocument splits doc.Content with c and returns the chunks
// stamped with doc.RelPath. Convenience wrapper used by the indexer
// to go from "loaded docs" to "ready-to-embed chunks" in one call.
func ChunkDocument(doc LoadedDocument, c RecursiveChunker) []Chunk {
	pieces := c.Split(doc.Content)
	out := make([]Chunk, len(pieces))
	for i, p := range pieces {
		out[i] = Chunk{DocPath: doc.RelPath, Index: i, Content: p}
	}
	return out
}
