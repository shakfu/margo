package core

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shakfu/margo/pkg/margo/mcp"
)

const fakeMCPArg = "margo-fake-mcp-server"

// TestHelperFakeMCPServer is not a test: re-executed as a subprocess
// with fakeMCPArg, it serves a one-tool MCP server on stdio.
func TestHelperFakeMCPServer(t *testing.T) {
	if !slices.Contains(os.Args, fakeMCPArg) {
		return
	}
	in := bufio.NewScanner(os.Stdin)
	out := json.NewEncoder(os.Stdout)
	for in.Scan() {
		var env mcp.Envelope
		if json.Unmarshal(in.Bytes(), &env) != nil || env.ID == nil {
			continue
		}
		var result any
		switch env.Method {
		case mcp.MethodInitialize:
			result = mcp.InitializeResult{
				ProtocolVersion: mcp.DefaultProtocolVersion,
				Capabilities:    mcp.ServerCapabilities{Tools: &mcp.ToolsCapability{}},
			}
		case mcp.MethodToolsList:
			result = mcp.ListToolsResult{Tools: []mcp.Tool{{Name: "echo"}}}
		default:
			result = struct{}{}
		}
		raw, _ := json.Marshal(result)
		_ = out.Encode(mcp.Envelope{JSONRPC: mcp.JSONRPCVersion, ID: env.ID, Result: raw})
	}
	os.Exit(0)
}

func fakeServerSpec(workspaces ...string) mcp.ServerSpec {
	return mcp.ServerSpec{
		Command:    os.Args[0],
		Args:       []string{"-test.run=^TestHelperFakeMCPServer$", "--", fakeMCPArg},
		Workspaces: workspaces,
	}
}

func waitReady(t *testing.T, s *Session, names ...string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for _, n := range names {
		for {
			srv := s.MCP().Server(n)
			if srv != nil {
				st, err := srv.Status()
				if st == mcp.StatusReady {
					break
				}
				if st == mcp.StatusFailed {
					t.Fatalf("server %s failed: %v; stderr: %v", n, err, srv.StderrTail())
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("server %s not ready in time", n)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// A server scoped to one workspace must not be listed for, or
// resolvable by, any other workspace or the no-workspace caller.
func TestMCPServerScopedToWorkspace(t *testing.T) {
	s := NewSession(Config{
		AttachmentRoot: t.TempDir(),
		CatalogDir:     t.TempDir(),
		MCPConfig: mcp.Config{MCPServers: map[string]mcp.ServerSpec{
			"shared": fakeServerSpec(),
			"proj":   fakeServerSpec("ws-a"),
		}},
	})
	t.Cleanup(s.Shutdown)
	waitReady(t, s, "shared", "proj")

	mcpNames := func(names []string) []string {
		out := []string{}
		for _, n := range names {
			if strings.HasPrefix(n, "mcp:") {
				out = append(out, n)
			}
		}
		return out
	}
	if got, want := mcpNames(s.Tools("ws-a")), []string{"mcp:proj:echo", "mcp:shared:echo"}; !slices.Equal(got, want) {
		t.Errorf("Tools(ws-a) MCP entries = %v, want %v", got, want)
	}
	for _, ws := range []string{"ws-b", ""} {
		if got, want := mcpNames(s.Tools(ws)), []string{"mcp:shared:echo"}; !slices.Equal(got, want) {
			t.Errorf("Tools(%q) MCP entries = %v, want %v", ws, got, want)
		}
		for _, m := range s.ToolsMetadata(context.Background(), ws) {
			if m.Name == "mcp:proj:echo" {
				t.Errorf("ToolsMetadata(%q) lists the ws-a server's tool", ws)
			}
		}
		if _, err := s.buildTools([]string{"mcp:proj:echo"}, ws); err == nil {
			t.Errorf("buildTools in %q resolved a tool scoped to ws-a", ws)
		}
	}
	if tools, err := s.buildTools([]string{"mcp:proj:echo", "mcp:shared:echo"}, "ws-a"); err != nil || len(tools) != 2 {
		t.Errorf("buildTools(ws-a) = %d tools, %v; want 2, nil", len(tools), err)
	}
}
