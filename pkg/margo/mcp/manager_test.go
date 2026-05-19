package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseQualified(t *testing.T) {
	cases := []struct {
		in                 string
		wantServer         string
		wantTool           string
		wantOK             bool
	}{
		{"mcp:fs:read", "fs", "read", true},
		{"mcp:github:search_repos", "github", "search_repos", true},
		{"mcp::tool", "", "tool", true}, // empty server is permitted by parser; manager validates
		{"current_time", "", "", false}, // builtin, not qualified
		{"mcp:only", "", "", false},     // missing tool half
		{"", "", "", false},
		{"mcp:", "", "", false},
	}
	for _, c := range cases {
		gotS, gotT, ok := ParseQualified(c.in)
		if ok != c.wantOK || gotS != c.wantServer || gotT != c.wantTool {
			t.Errorf("ParseQualified(%q) = (%q, %q, %v); want (%q, %q, %v)",
				c.in, gotS, gotT, ok, c.wantServer, c.wantTool, c.wantOK)
		}
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if cfg.MCPServers == nil {
		t.Errorf("MCPServers should be initialised to empty map, got nil")
	}
	if len(cfg.MCPServers) != 0 {
		t.Errorf("missing file should yield empty map, got %v", cfg.MCPServers)
	}
}

func TestLoadConfigValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(path, []byte(`{
		"mcpServers": {
			"fs": {
				"command": "npx",
				"args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"],
				"env": {"FOO": "bar"}
			}
		}
	}`), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	fs, ok := cfg.MCPServers["fs"]
	if !ok {
		t.Fatalf("expected fs server in config, got %v", cfg.MCPServers)
	}
	if fs.Command != "npx" || len(fs.Args) != 3 || fs.Env["FOO"] != "bar" {
		t.Errorf("config not parsed correctly: %+v", fs)
	}
}

func TestLoadConfigMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Errorf("malformed JSON should error")
	}
}

// TestAddServerWithEmptyCommandFailsFast exercises the early-failure
// path: a server spec with no command should land in StatusFailed
// without spawning anything.
func TestAddServerWithEmptyCommandFailsFast(t *testing.T) {
	m := NewManager(nil)
	defer m.StopAll(time.Second)

	srv := m.AddServer(context.Background(), "bad", ServerSpec{})
	// addAndStart returns the placeholder synchronously. Poll for the
	// real server to replace it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		live := m.Server("bad")
		if live != nil {
			st, _ := live.Status()
			if st == StatusFailed {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	st, err := srv.Status()
	t.Fatalf("server did not reach Failed in time; final status=%s err=%v", st, err)
}

// TestAddServerWithMissingBinaryFailsFast points the manager at a
// command that doesn't exist on PATH. Same shape as the empty-command
// test but exercises exec.Cmd.Start's error path.
func TestAddServerWithMissingBinaryFailsFast(t *testing.T) {
	m := NewManager(nil)
	defer m.StopAll(time.Second)

	m.AddServer(context.Background(), "ghost", ServerSpec{
		Command: "this-binary-definitely-does-not-exist-xyz",
	})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		live := m.Server("ghost")
		if live == nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		st, err := live.Status()
		if st == StatusFailed {
			if err == nil {
				t.Errorf("Failed status should carry an error")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("missing binary did not reach Failed in time")
}

func TestStopAllEmptyManager(t *testing.T) {
	m := NewManager(nil)
	m.StopAll(time.Second) // should not panic / hang
	if got := m.Servers(); len(got) != 0 {
		t.Errorf("StopAll did not clear registry, got %d entries", len(got))
	}
}

func TestRingLogRotation(t *testing.T) {
	r := newRingLog(3)
	r.add("a")
	r.add("b")
	r.add("c")
	r.add("d")
	got := r.snapshot()
	want := []string{"b", "c", "d"}
	if len(got) != 3 {
		t.Fatalf("ring snapshot length = %d, want 3", len(got))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("ring[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestManagerToolsAggregation builds a synthetic Server with known
// tools and asserts Tools() namespacing. Uses a manual server insertion
// so we don't need a live subprocess.
func TestManagerToolsAggregation(t *testing.T) {
	m := NewManager(nil)
	s1 := &Server{
		name:         "alpha",
		status:       StatusReady,
		tools:        []Tool{{Name: "one"}, {Name: "two"}},
		exitWaitDone: closedChan(),
	}
	s2 := &Server{
		name:         "beta",
		status:       StatusReady,
		tools:        []Tool{{Name: "three"}},
		exitWaitDone: closedChan(),
	}
	s3 := &Server{
		name:         "gamma",
		status:       StatusFailed,
		tools:        []Tool{{Name: "ghost"}},
		exitWaitDone: closedChan(),
	}
	m.mu.Lock()
	m.servers["alpha"] = s1
	m.servers["beta"] = s2
	m.servers["gamma"] = s3
	m.mu.Unlock()

	got := m.Tools()
	wantQualified := []string{"mcp:alpha:one", "mcp:alpha:two", "mcp:beta:three"}
	if len(got) != len(wantQualified) {
		t.Fatalf("Tools() returned %d entries, want %d: %+v", len(got), len(wantQualified), got)
	}
	for i, w := range wantQualified {
		if got[i].Qualified != w {
			t.Errorf("Tools()[%d].Qualified = %q, want %q", i, got[i].Qualified, w)
		}
	}
	// Gamma (failed) must not appear.
	for _, nt := range got {
		if strings.HasPrefix(nt.Qualified, "mcp:gamma:") {
			t.Errorf("failed server %q surfaced a tool: %s", "gamma", nt.Qualified)
		}
	}
}

func closedChan() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

// TestCallQualifiedRoutesAndRejects exercises name parsing and
// not-ready guards without needing a live subprocess.
func TestCallQualifiedRoutesAndRejects(t *testing.T) {
	m := NewManager(nil)
	ctx := context.Background()

	if _, err := m.CallQualified(ctx, "not_qualified", nil); err == nil {
		t.Errorf("unqualified name should be rejected")
	}
	if _, err := m.CallQualified(ctx, "mcp:absent:tool", nil); err == nil {
		t.Errorf("unknown server should be rejected")
	}
	// A registered but not-ready server should reject too.
	m.mu.Lock()
	m.servers["pending"] = &Server{name: "pending", status: StatusStarting, exitWaitDone: make(chan struct{})}
	m.mu.Unlock()
	if _, err := m.CallQualified(ctx, "mcp:pending:tool", json.RawMessage(`{}`)); err == nil {
		t.Errorf("not-ready server should be rejected")
	}
}
