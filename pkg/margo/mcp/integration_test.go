//go:build integration

// Integration tests for the MCP MVP. Run with:
//
//	go test -tags=integration -v -timeout=2m ./pkg/margo/mcp/
//
// These tests spawn a real MCP server subprocess (community
// @modelcontextprotocol/server-filesystem via npx) to verify the wire
// protocol end-to-end. Skipped without the build tag and skipped at
// run-time if npx is unavailable so they don't fail on machines
// without Node.
//
// First run downloads the npm package into the user's npm cache, which
// can take 10–30 seconds; subsequent runs are fast. The cold-start
// timeout below is generous to account for this.

package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// requireNpx skips the test if npx isn't on PATH. Returns the resolved
// command so tests can construct ServerSpec with confidence.
func requireNpx(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("npx")
	if err != nil {
		t.Skipf("npx not on PATH: %v", err)
	}
	return path
}

// waitForReady polls a Manager-resolved Server's Status until it
// reaches Ready, Failed, or the timeout fires. Returns the terminal
// status + error.
//
// IMPORTANT: re-resolves the server via mgr.Server(name) on each
// iteration rather than caching a *Server pointer. The manager
// inserts a placeholder Server during addAndStart and swaps in the
// live one only after startServer returns; a cached pointer at the
// placeholder would never observe the status transition.
func waitForReady(t *testing.T, mgr *Manager, name string, timeout time.Duration) (ServerStatus, error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if srv := mgr.Server(name); srv != nil {
			st, err := srv.Status()
			if st == StatusReady || st == StatusFailed {
				return st, err
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	srv := mgr.Server(name)
	var (
		st     ServerStatus
		err    error
		stderr []string
	)
	if srv != nil {
		st, err = srv.Status()
		stderr = srv.StderrTail()
	}
	t.Fatalf("server %q did not reach Ready/Failed within %s; final status=%s err=%v stderr=%v",
		name, timeout, st, err, stderr)
	return st, err // unreachable
}

// TestE2EFilesystemServerListTools spawns the real server-filesystem
// MCP server pointed at a temp directory and verifies the standard
// tools (read_file, write_file, list_directory, …) show up via
// tools/list. This exercises:
//
//   - subprocess spawn + stdio piping
//   - initialize handshake with a real server's protocol negotiation
//   - tools/list response parsing
//   - the manager's namespacing layer
//
// Anything subtle on the wire (protocol version mismatch, schema
// dialect differences, BOMs, etc.) shows up here in a way no
// in-pipe fake can simulate.
func TestE2EFilesystemServerListTools(t *testing.T) {
	requireNpx(t)

	rootDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootDir, "hello.txt"), []byte("smoke-test content"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	mgr := NewManager(nil)
	defer mgr.StopAll(5 * time.Second)

	mgr.AddServer(context.Background(), "fs", ServerSpec{
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", rootDir},
	})

	// Cold start downloads the npm package on first run; subsequent
	// runs are fast. 60s is generous.
	st, err := waitForReady(t, mgr, "fs", 60*time.Second)
	if st != StatusReady {
		t.Fatalf("server failed: status=%s err=%v stderr=%v", st, err, mgr.Server("fs").StderrTail())
	}
	srv := mgr.Server("fs")

	tools := srv.Tools()
	if len(tools) == 0 {
		t.Fatalf("server returned no tools")
	}

	// The server-filesystem package exposes a stable set of tools;
	// require at minimum read_file + list_directory (or list_allowed_directories
	// on newer versions). We assert on names we can verify on every release.
	wantAtLeast := []string{"read_file"}
	for _, name := range wantAtLeast {
		if !hasToolNamed(tools, name) {
			t.Errorf("expected tool %q in catalog, got: %v", name, toolNames(tools))
		}
	}

	// Namespaced view should match.
	nts := mgr.Tools()
	if len(nts) == 0 {
		t.Fatalf("Manager.Tools() returned empty")
	}
	for _, nt := range nts {
		if !strings.HasPrefix(nt.Qualified, "mcp:fs:") {
			t.Errorf("qualified name should namespace under fs, got %q", nt.Qualified)
		}
	}
}

// TestE2EFilesystemServerReadFile drives a real tools/call round-trip
// against the same server. Seeds a known file in the temp root,
// invokes read_file, and asserts the content comes back verbatim in
// the response's first text block.
//
// This is the load-bearing test for the MCP MVP: if it passes, the
// wire protocol is good enough that a model invoking an MCP tool
// will receive the right bytes back. If it fails, anything else we
// build on top is suspect.
func TestE2EFilesystemServerReadFile(t *testing.T) {
	requireNpx(t)

	// macOS resolves /var/folders/... → /private/var/folders/...
	// via symlink. server-filesystem canonicalizes its allowed
	// directory at startup and then rejects any path that doesn't
	// share the canonical prefix. Resolve our temp dir to the same
	// canonical form so the server accepts file paths we hand it.
	rootDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	const want = "smoke-test content"
	filePath := filepath.Join(rootDir, "hello.txt")
	if err := os.WriteFile(filePath, []byte(want), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	mgr := NewManager(nil)
	defer mgr.StopAll(5 * time.Second)

	mgr.AddServer(context.Background(), "fs", ServerSpec{
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", rootDir},
	})

	st, werr := waitForReady(t, mgr, "fs", 60*time.Second)
	if st != StatusReady {
		t.Fatalf("server failed: status=%s err=%v", st, werr)
	}

	// The arguments shape varies slightly across server-filesystem
	// versions; the 2025+ schema uses {"path": "..."} and accepts
	// either absolute or root-relative. We pass absolute since
	// that's least ambiguous.
	args, _ := json.Marshal(map[string]string{"path": filePath})
	res, err := mgr.CallQualified(context.Background(), "mcp:fs:read_file", args)
	if err != nil {
		t.Fatalf("CallQualified: %v\nstderr tail: %v", err, mgr.Server("fs").StderrTail())
	}
	if res.IsError {
		t.Fatalf("read_file returned tool-level error: %+v", res.Content)
	}
	if len(res.Content) == 0 {
		t.Fatalf("empty content")
	}

	// Find the first text block. Some servers prefix with metadata
	// (e.g. mime block); we tolerate by scanning all blocks.
	got := ""
	for _, b := range res.Content {
		if b.Type == "text" || b.Type == "" {
			got = b.Text
			break
		}
	}
	if !strings.Contains(got, want) {
		t.Errorf("read_file content mismatch.\n  want substring: %q\n  got: %q", want, got)
	}
}

// TestE2EFilesystemServerCleanShutdown verifies that StopAll
// terminates the subprocess promptly. Without the polite-shutdown
// chain (close stdin → server sees EOF → exits → cmd.Wait completes),
// a busy server could hang the test forever.
func TestE2EFilesystemServerCleanShutdown(t *testing.T) {
	requireNpx(t)

	rootDir := t.TempDir()
	mgr := NewManager(nil)
	mgr.AddServer(context.Background(), "fs", ServerSpec{
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", rootDir},
	})
	st, _ := waitForReady(t, mgr, "fs", 60*time.Second)
	if st != StatusReady {
		t.Fatalf("server failed pre-shutdown: %s", st)
	}

	done := make(chan struct{})
	go func() {
		mgr.StopAll(5 * time.Second)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("StopAll did not return within 10s; subprocess likely orphaned")
	}
}

func hasToolNamed(tools []Tool, name string) bool {
	for _, t := range tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

func toolNames(tools []Tool) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}
