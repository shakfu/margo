package main

import (
	"context"
	"testing"
)

// TestToolsMetadataShape exercises the Wails-facing ToolsMetadata
// contract: every entry has a name, the read-only flag matches the
// agent.ReadOnlyTools map, and the streamable flag is true exactly
// for tools that implement tool.StreamableTool (web_fetch, today).
func TestToolsMetadataShape(t *testing.T) {
	a := NewApp()
	a.ctx = context.Background()
	got := a.ToolsMetadata()
	if len(got) == 0 {
		t.Fatalf("expected at least one tool registered")
	}

	byName := make(map[string]ToolMetadata, len(got))
	for _, m := range got {
		if m.Name == "" {
			t.Errorf("entry has empty Name: %+v", m)
		}
		byName[m.Name] = m
	}

	// current_time is a stable read-only baseline.
	ct, ok := byName["current_time"]
	if !ok {
		t.Fatalf("current_time not in ToolsMetadata: keys=%v", keysOf(byName))
	}
	if !ct.IsReadOnly {
		t.Errorf("current_time should be marked read-only")
	}
	if ct.IsStreamable {
		t.Errorf("current_time is invokable, not streamable")
	}
	if ct.Description == "" {
		t.Errorf("current_time should expose a description")
	}

	// web_fetch is the canonical streamable tool.
	wf, ok := byName["web_fetch"]
	if !ok {
		t.Fatalf("web_fetch not in ToolsMetadata")
	}
	if !wf.IsStreamable {
		t.Errorf("web_fetch should be marked streamable")
	}
	if wf.IsReadOnly {
		t.Errorf("web_fetch performs network I/O; should not be auto-approved")
	}
}

// TestToolsMetadataSortedByName guarantees a deterministic catalog
// order so the UI doesn't reshuffle the Tools tab on each refresh.
func TestToolsMetadataSortedByName(t *testing.T) {
	a := NewApp()
	a.ctx = context.Background()
	got := a.ToolsMetadata()
	for i := 1; i < len(got); i++ {
		if got[i-1].Name > got[i].Name {
			t.Errorf("ToolsMetadata not sorted: %q precedes %q", got[i-1].Name, got[i].Name)
		}
	}
}

func keysOf(m map[string]ToolMetadata) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
