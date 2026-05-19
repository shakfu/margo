package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestAdapterInfoUsesQualifiedName(t *testing.T) {
	m := NewManager(nil)
	nt := NamespacedTool{
		Server: "fs",
		Tool: Tool{
			Name:        "read",
			Description: "Read a file",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
		},
		Qualified: "mcp:fs:read",
	}
	tt := AsEinoTool(m, nt)
	info, err := tt.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Name != "mcp:fs:read" {
		t.Errorf("Info.Name = %q, want mcp:fs:read", info.Name)
	}
	if info.Desc != "Read a file" {
		t.Errorf("Info.Desc = %q, want %q", info.Desc, "Read a file")
	}
	if info.ParamsOneOf == nil {
		t.Errorf("ParamsOneOf should be populated from inputSchema")
	}
}

func TestAdapterInfoFallsBackOnMissingDescription(t *testing.T) {
	m := NewManager(nil)
	nt := NamespacedTool{
		Server:    "x",
		Tool:      Tool{Name: "y"},
		Qualified: "mcp:x:y",
	}
	info, err := AsEinoTool(m, nt).Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if !strings.Contains(info.Desc, "MCP tool") {
		t.Errorf("missing description should yield fallback, got %q", info.Desc)
	}
}

func TestAdapterInfoSurvivesBadSchema(t *testing.T) {
	m := NewManager(nil)
	nt := NamespacedTool{
		Server:    "x",
		Tool:      Tool{Name: "y", InputSchema: json.RawMessage(`not-json`)},
		Qualified: "mcp:x:y",
	}
	info, err := AsEinoTool(m, nt).Info(context.Background())
	if err != nil {
		t.Fatalf("bad schema should not error from Info, got %v", err)
	}
	if info.ParamsOneOf != nil {
		t.Errorf("bad schema should leave ParamsOneOf nil (no-args fallback)")
	}
}

func TestFlattenContent(t *testing.T) {
	got := flattenContent([]Content{
		{Type: "text", Text: "first"},
		{Type: "text", Text: "second"},
	})
	if got != "first\n\nsecond" {
		t.Errorf("text concatenation wrong: %q", got)
	}

	got = flattenContent([]Content{
		{Type: "image", MimeType: "image/png", Data: "AAAA"},
	})
	if !strings.HasPrefix(got, "[image: image/png") {
		t.Errorf("image placeholder missing, got %q", got)
	}

	if flattenContent(nil) != "" {
		t.Errorf("nil content should yield empty string")
	}
}
