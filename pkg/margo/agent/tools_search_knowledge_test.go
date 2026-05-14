package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"github.com/shakfu/margo/pkg/margo/rag"
)

type stubSearch struct {
	results []rag.QueryResult
	err     error
	lastQ   string
	lastK   int
}

func (s *stubSearch) Search(_ context.Context, q string, k int) ([]rag.QueryResult, error) {
	s.lastQ, s.lastK = q, k
	return s.results, s.err
}

func invokeJSON(t *testing.T, tt tool.InvokableTool, args string) string {
	t.Helper()
	out, err := tt.InvokableRun(context.Background(), args)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	return out
}

func TestSearchKnowledgeFormatsResults(t *testing.T) {
	p := &stubSearch{results: []rag.QueryResult{
		{Document: rag.Document{Content: "first chunk", Metadata: map[string]string{"source": "/abs/a.md", "doc": "a.md"}}, Score: 0.91},
		{Document: rag.Document{Content: "second chunk", Metadata: map[string]string{"source": "/abs/b.md", "doc": "b.md"}}, Score: 0.42},
	}}
	tt := SearchKnowledgeTool(p)
	out := invokeJSON(t, tt, `{"query":"alpha","k":2}`)
	if p.lastQ != "alpha" || p.lastK != 2 {
		t.Errorf("provider received q=%q k=%d", p.lastQ, p.lastK)
	}
	for _, want := range []string{"first chunk", "second chunk", "/abs/a.md", "/abs/b.md", "0.910", "0.420"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %s", want, out)
		}
	}
}

func TestSearchKnowledgeDefaultK(t *testing.T) {
	p := &stubSearch{}
	tt := SearchKnowledgeTool(p)
	invokeJSON(t, tt, `{"query":"alpha"}`)
	if p.lastK != DefaultSearchKnowledgeK {
		t.Errorf("default k: got %d, want %d", p.lastK, DefaultSearchKnowledgeK)
	}
}

func TestSearchKnowledgeNoProvider(t *testing.T) {
	tt := SearchKnowledgeTool(nil)
	out := invokeJSON(t, tt, `{"query":"alpha"}`)
	if !strings.Contains(out, "no knowledge index") {
		t.Errorf("expected friendly no-index message, got %q", out)
	}
}

func TestSearchKnowledgeEmptyResults(t *testing.T) {
	p := &stubSearch{}
	tt := SearchKnowledgeTool(p)
	out := invokeJSON(t, tt, `{"query":"alpha"}`)
	if !strings.Contains(out, "no matches") {
		t.Errorf("expected no-matches message, got %q", out)
	}
}

func TestSearchKnowledgePublishesHits(t *testing.T) {
	p := &stubSearch{results: []rag.QueryResult{
		{Document: rag.Document{Content: "alpha bravo charlie delta echo foxtrot golf hotel india juliet", Metadata: map[string]string{"source": "/abs/a.md", "doc": "a.md"}}, Score: 0.81},
		{Document: rag.Document{Content: "second body", Metadata: map[string]string{"source": "/abs/b.md", "doc": "b.md"}}, Score: 0.42},
	}}
	tt := SearchKnowledgeTool(p)

	var got []StepEvent
	emit := func(ev StepEvent) { got = append(got, ev) }
	ctx := WithStepEmitter(context.Background(), emit)
	if _, err := tt.InvokableRun(ctx, `{"query":"alpha"}`); err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}

	var retrieve *StepEvent
	for i, ev := range got {
		if ev.Kind == StepRetrieve {
			retrieve = &got[i]
			break
		}
	}
	if retrieve == nil {
		t.Fatalf("expected a StepRetrieve event, got %+v", got)
	}
	if retrieve.Name != "search_knowledge" {
		t.Errorf("retrieve.Name = %q, want search_knowledge", retrieve.Name)
	}
	if len(retrieve.Hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(retrieve.Hits))
	}
	if retrieve.Hits[0].Path != "/abs/a.md" || retrieve.Hits[0].Doc != "a.md" || retrieve.Hits[0].Score != 0.81 {
		t.Errorf("hit[0] = %+v", retrieve.Hits[0])
	}
	if retrieve.Hits[0].Snippet == "" {
		t.Errorf("hit[0].Snippet should be populated")
	}
}

func TestSearchKnowledgeNoEmitterIsHarmless(t *testing.T) {
	// Calling PublishStep against a vanilla context (no emitter installed)
	// must not panic; this is the path tests and CLI invocations take.
	p := &stubSearch{results: []rag.QueryResult{
		{Document: rag.Document{Content: "x", Metadata: map[string]string{"source": "/a"}}, Score: 0.1},
	}}
	tt := SearchKnowledgeTool(p)
	if _, err := tt.InvokableRun(context.Background(), `{"query":"alpha"}`); err != nil {
		t.Fatalf("InvokableRun without emitter: %v", err)
	}
}
