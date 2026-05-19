package mcp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// ServerSpec describes one MCP server entry from the Claude-Desktop-
// compatible config file (mcpServers map). Servers are launched as
// subprocesses with their stdio piped into a Client.
//
// The shape mirrors Claude Desktop's mcp.json so users can copy-paste
// existing configs without translation:
//
//	{
//	  "command": "npx",
//	  "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path"],
//	  "env":  {"GITHUB_TOKEN": "..."},
//	  "cwd":  "/optional/working/dir"
//	}
type ServerSpec struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	CWD     string            `json:"cwd,omitempty"`
}

// ServerStatus is the lifecycle state of one managed server. Drives the
// UI's "is this server up?" indicator and the manager's reconciler.
type ServerStatus string

const (
	StatusStarting ServerStatus = "starting"
	StatusReady    ServerStatus = "ready"
	StatusFailed   ServerStatus = "failed"
	StatusStopped  ServerStatus = "stopped"
)

// Server wraps one running MCP subprocess + its Client. It owns the
// exec.Cmd, the Client, and a small ring-buffered log of stderr lines
// so the UI can show "what went wrong" without a separate log tail.
//
// Server is constructed by Manager.Start; callers normally interact
// with it indirectly via the Manager. The Tools() / CallTool() methods
// are forwarded so the agent runner can treat a Server like any tool
// host.
type Server struct {
	name string
	spec ServerSpec

	cmd    *exec.Cmd
	client *Client

	logger *log.Logger
	stderr *ringLog // last N lines from the subprocess's stderr

	mu           sync.RWMutex
	status       ServerStatus
	statusErr    error      // last error that drove a status transition (e.g. crash cause)
	tools        []Tool     // last fetched tool catalog
	startedAt    time.Time
	exitWaitDone chan struct{} // closed when cmd.Wait returns

	closing atomic.Bool
}

// Tools returns the last-fetched tool catalog. Empty until the
// initialize+listTools sequence has completed (Manager.Start blocks
// the goroutine until either ready or failed, but other callers may
// race).
func (s *Server) Tools() []Tool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Return a copy so the caller can't mutate our cache.
	out := make([]Tool, len(s.tools))
	copy(out, s.tools)
	return out
}

// Status returns the current lifecycle state plus the last status-
// transition error (nil when status is Ready). Cheap; safe to poll.
func (s *Server) Status() (ServerStatus, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status, s.statusErr
}

// Name returns the user-facing server name (from the mcpServers map key).
func (s *Server) Name() string { return s.name }

// StderrTail returns the last N stderr lines captured from the
// subprocess. Useful for surfacing "why did this fail" in the UI.
func (s *Server) StderrTail() []string {
	if s.stderr == nil {
		return nil
	}
	return s.stderr.snapshot()
}

// CallTool forwards to the underlying client. Returns an error if the
// server isn't ready (e.g. still starting, or already failed).
func (s *Server) CallTool(ctx context.Context, name string, args []byte) (*CallToolResult, error) {
	s.mu.RLock()
	status := s.status
	client := s.client
	s.mu.RUnlock()
	if status != StatusReady {
		return nil, fmt.Errorf("mcp server %q not ready (status=%s)", s.name, status)
	}
	return client.CallTool(ctx, name, args)
}

// Stop terminates the server. Closes the client first (which sends EOF
// on stdin, prompting a well-behaved server to exit), then waits up to
// stopTimeout for cmd.Wait. Kills the subprocess after the timeout.
// Idempotent; safe to call from multiple goroutines.
func (s *Server) Stop(stopTimeout time.Duration) error {
	if s.closing.Swap(true) {
		return nil
	}
	s.setStatus(StatusStopped, nil)

	// 1. Close stdin via client.Close. Polite shutdown.
	if s.client != nil {
		_ = s.client.Close()
	}

	// 2. Give the subprocess up to stopTimeout to exit; SIGKILL otherwise.
	if s.cmd != nil && s.cmd.Process != nil {
		select {
		case <-s.exitWaitDone:
		case <-time.After(stopTimeout):
			s.logger.Printf("mcp[%s]: stop timeout, killing subprocess", s.name)
			_ = s.cmd.Process.Kill()
			<-s.exitWaitDone
		}
	}
	return nil
}

func (s *Server) setStatus(st ServerStatus, err error) {
	s.mu.Lock()
	s.status = st
	s.statusErr = err
	s.mu.Unlock()
}

// startServer launches the subprocess, wires its stdio to a Client,
// runs the initialize+listTools handshake, and returns the Server.
// All errors leave the Server in StatusFailed with statusErr set,
// rather than returning nil — the Manager wants to keep the row so the
// UI can show the failure.
func startServer(ctx context.Context, name string, spec ServerSpec, logger *log.Logger) *Server {
	srv := &Server{
		name:         name,
		spec:         spec,
		logger:       logger,
		stderr:       newRingLog(50),
		status:       StatusStarting,
		startedAt:    time.Now(),
		exitWaitDone: make(chan struct{}),
	}

	if spec.Command == "" {
		srv.setStatus(StatusFailed, errors.New("empty command"))
		close(srv.exitWaitDone)
		return srv
	}

	cmd := exec.CommandContext(ctx, spec.Command, spec.Args...)
	if spec.CWD != "" {
		cmd.Dir = spec.CWD
	}
	if len(spec.Env) > 0 {
		envList := make([]string, 0, len(spec.Env))
		for k, v := range spec.Env {
			envList = append(envList, k+"="+v)
		}
		// Inherit ambient env then override.
		cmd.Env = append(cmd.Env, envList...)
	}
	srv.cmd = cmd

	stdin, err := cmd.StdinPipe()
	if err != nil {
		srv.setStatus(StatusFailed, fmt.Errorf("stdin pipe: %w", err))
		close(srv.exitWaitDone)
		return srv
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		srv.setStatus(StatusFailed, fmt.Errorf("stdout pipe: %w", err))
		close(srv.exitWaitDone)
		return srv
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		srv.setStatus(StatusFailed, fmt.Errorf("stderr pipe: %w", err))
		close(srv.exitWaitDone)
		return srv
	}

	if err := cmd.Start(); err != nil {
		srv.setStatus(StatusFailed, fmt.Errorf("start %s: %w", spec.Command, err))
		close(srv.exitWaitDone)
		return srv
	}

	// Drain stderr into the ring log. The goroutine exits on EOF.
	go drainStderr(srv.stderr, stderr, logger, name)

	// Reap the subprocess in the background. Any waiter on Stop blocks
	// on exitWaitDone; the channel closes whether the process exits
	// cleanly or is killed.
	go func() {
		err := cmd.Wait()
		if err != nil && !srv.closing.Load() {
			srv.setStatus(StatusFailed, fmt.Errorf("subprocess exited: %w", err))
			logger.Printf("mcp[%s]: subprocess exited unexpectedly: %v", name, err)
		}
		close(srv.exitWaitDone)
	}()

	srv.client = NewClient(name, stdin, stdout, Options{Logger: logger})

	// Initialize + first tools/list. Both block the Start goroutine
	// until ready or failed, but the manager spawns Start in its own
	// goroutine per server, so eager+async overall is preserved.
	if err := srv.client.Initialize(ctx); err != nil {
		srv.setStatus(StatusFailed, fmt.Errorf("initialize: %w", err))
		return srv
	}
	if caps := srv.client.ServerCapabilities(); caps.Tools != nil {
		tools, err := srv.client.ListTools(ctx)
		if err != nil {
			srv.setStatus(StatusFailed, fmt.Errorf("tools/list: %w", err))
			return srv
		}
		srv.mu.Lock()
		srv.tools = tools
		srv.mu.Unlock()
	}
	srv.setStatus(StatusReady, nil)
	logger.Printf("mcp[%s]: ready (%d tools)", name, len(srv.Tools()))
	return srv
}

// drainStderr reads the subprocess's stderr line-by-line into the ring
// buffer. Also forwards each line to the structured logger so margo's
// own log file can show interleaved MCP server output.
func drainStderr(ring *ringLog, r io.Reader, logger *log.Logger, name string) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		ring.add(line)
		logger.Printf("mcp[%s] stderr: %s", name, line)
	}
}

// ringLog is a tiny thread-safe ring buffer of strings. The MCP MVP
// uses it to keep the last ~50 stderr lines so the UI's "why is this
// failing" affordance has something to render without a separate log
// file.
type ringLog struct {
	mu    sync.Mutex
	items []string
	cap   int
}

func newRingLog(cap int) *ringLog { return &ringLog{cap: cap} }

func (r *ringLog) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.items) >= r.cap {
		// Drop oldest. Cheap because cap is small.
		r.items = append(r.items[1:], s)
		return
	}
	r.items = append(r.items, s)
}

func (r *ringLog) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.items))
	copy(out, r.items)
	return out
}
