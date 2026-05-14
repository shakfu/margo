package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"

	"github.com/shakfu/margo/pkg/margo/rag"
)

// SearchProvider is the narrow subset of *rag.Indexer the search_knowledge
// tool needs. Defined as an interface (rather than taking *rag.Indexer
// directly) so tests can substitute a fake without spinning up a real
// embedder + store.
type SearchProvider interface {
	Search(ctx context.Context, query string, k int) ([]rag.QueryResult, error)
}

// DefaultSearchKnowledgeK is the fallback k when the model omits it. Five
// chunks is enough to surface multiple relevant passages without dominating
// the next prompt; the model can request more by passing an explicit k.
const DefaultSearchKnowledgeK = 5

// snippet returns the first max runes of s with whitespace runs collapsed
// to single spaces. Used to keep retrieval-hit cards compact in the UI
// without stripping the matching content the model still receives in full.
func snippet(s string, max int) string {
	var b strings.Builder
	b.Grow(max)
	prevSpace := false
	count := 0
	for _, r := range s {
		isSpace := r == ' ' || r == '\t' || r == '\n' || r == '\r'
		if isSpace {
			if prevSpace || b.Len() == 0 {
				continue
			}
			b.WriteByte(' ')
			prevSpace = true
		} else {
			b.WriteRune(r)
			prevSpace = false
		}
		count++
		if count >= max {
			b.WriteString("…")
			break
		}
	}
	return strings.TrimSpace(b.String())
}

type searchKnowledgeArgs struct {
	Query string `json:"query" jsonschema:"description=Natural-language question or keyword to search the indexed knowledge for"`
	K     int    `json:"k,omitempty" jsonschema:"description=Maximum number of chunks to return; defaults to 5"`
}

// SearchKnowledgeTool returns an InvokableTool that queries `provider` for
// chunks similar to the model's query and formats them as a text block. The
// provider is captured at construction time so this tool is always wired to
// a specific workspace's index (callers rebuild it when the active workspace
// changes).
//
// Provider may be nil — in that case the tool returns a clear "no index
// configured" message rather than the tool registration failing. This is
// the path users without an OpenAI key hit if they enable the tool without
// indexing anything.
func SearchKnowledgeTool(provider SearchProvider) tool.InvokableTool {
	t, err := toolutils.InferTool(
		"search_knowledge",
		"Search the workspace's indexed knowledge for chunks similar to a query. Use when the user asks about documents, code, or notes in their workspace. Returns up to k chunks with a source path and similarity score.",
		func(ctx context.Context, in searchKnowledgeArgs) (string, error) {
			if provider == nil {
				return "search_knowledge: no knowledge index is configured for the active workspace. Index a file or folder from Settings → Knowledge sources first.", nil
			}
			if strings.TrimSpace(in.Query) == "" {
				return "", errors.New("query is required")
			}
			k := in.K
			if k <= 0 {
				k = DefaultSearchKnowledgeK
			}
			results, err := provider.Search(ctx, in.Query, k)
			if err != nil {
				return "", err
			}
			if len(results) == 0 {
				return "search_knowledge: no matches.", nil
			}
			hits := make([]RetrievalHit, len(results))
			var b strings.Builder
			for i, r := range results {
				source := r.Metadata["source"]
				doc := r.Metadata["doc"]
				if source == "" {
					source = "(unknown)"
				}
				hits[i] = RetrievalHit{
					Path:    source,
					Doc:     doc,
					Score:   r.Score,
					Snippet: snippet(r.Content, 240),
				}
				fmt.Fprintf(&b, "[%d] %s", i+1, source)
				if doc != "" && doc != source {
					fmt.Fprintf(&b, " :: %s", doc)
				}
				fmt.Fprintf(&b, "  (score=%.3f)\n", r.Score)
				b.WriteString(r.Content)
				if i < len(results)-1 {
					b.WriteString("\n---\n")
				}
			}
			// Surface a structured StepRetrieve event so the UI can render
			// the hit list as cards. The text return (`b.String()`) still
			// goes back through the normal tool_result path, which is what
			// the model continues reasoning over.
			PublishStep(ctx, StepEvent{Kind: StepRetrieve, Name: "search_knowledge", Hits: hits})
			return b.String(), nil
		},
	)
	if err != nil {
		panic(err)
	}
	return t
}
