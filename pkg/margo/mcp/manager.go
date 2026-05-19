package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Manager owns the lifecycle of one or more MCP servers. The MVP
// supports a global registry (every workspace sees the same servers);
// per-workspace scoping is a future slice that will wrap this manager.
//
// Lifecycle convention is eager+async: StartAll launches every server
// in parallel and returns immediately. Tools and CallTool callers
// observe StatusStarting until each server's initialize+listTools
// completes. Failures are sticky on the Server row so the UI can
// render them; the manager does not auto-restart.
type Manager struct {
	logger *log.Logger

	mu      sync.RWMutex
	servers map[string]*Server // name -> Server (failed servers are kept)
}

// NewManager constructs a manager with a logger sink. Pass a discarding
// logger to silence; pass log.Default() (or a file-backed *log.Logger)
// to capture per-server stderr + lifecycle events.
func NewManager(logger *log.Logger) *Manager {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Manager{
		logger:  logger,
		servers: map[string]*Server{},
	}
}

// Config is the JSON shape margo loads from <UserConfigDir>/Margo/mcp.json
// to populate the manager. Compatible with Claude Desktop's mcp.json.
type Config struct {
	MCPServers map[string]ServerSpec `json:"mcpServers"`
}

// LoadConfig reads the JSON config from the given path. A missing file
// is not an error — the manager runs with an empty registry until the
// user creates one. Returns an empty Config{} in that case.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Config{MCPServers: map[string]ServerSpec{}}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read mcp config: %w", err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("parse mcp config %s: %w", path, err)
	}
	if c.MCPServers == nil {
		c.MCPServers = map[string]ServerSpec{}
	}
	return c, nil
}

// DefaultConfigPath returns the canonical path margo reads MCP config
// from: <UserConfigDir>/Margo/mcp.json. The directory is created on
// demand by SaveConfig.
func DefaultConfigPath() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "Margo", "mcp.json"), nil
}

// StartAll launches every server in cfg in parallel goroutines. Returns
// immediately; callers observe per-server status via Servers() or
// Status(name). A nil cfg or empty MCPServers is a no-op.
//
// Starting an already-managed server is a no-op (no double-start). Add
// a server via AddServer if it's not already in the manager.
func (m *Manager) StartAll(ctx context.Context, cfg Config) {
	for name, spec := range cfg.MCPServers {
		m.addAndStart(ctx, name, spec)
	}
}

// AddServer registers and launches one server. Returns the live Server
// regardless of outcome — the caller reads Status() to detect failure.
// If a server with the same name already exists, returns the existing
// one without re-launching.
func (m *Manager) AddServer(ctx context.Context, name string, spec ServerSpec) *Server {
	return m.addAndStart(ctx, name, spec)
}

func (m *Manager) addAndStart(ctx context.Context, name string, spec ServerSpec) *Server {
	m.mu.Lock()
	if existing, ok := m.servers[name]; ok {
		m.mu.Unlock()
		return existing
	}
	// Reserve the slot with a placeholder so concurrent AddServer calls
	// for the same name only spawn one subprocess. We swap the
	// placeholder with the real Server inside the goroutine.
	placeholder := &Server{
		name:         name,
		spec:         spec,
		status:       StatusStarting,
		startedAt:    time.Now(),
		exitWaitDone: make(chan struct{}),
		logger:       m.logger,
		stderr:       newRingLog(50),
	}
	m.servers[name] = placeholder
	m.mu.Unlock()

	go func() {
		live := startServer(ctx, name, spec, m.logger)
		m.mu.Lock()
		m.servers[name] = live
		m.mu.Unlock()
		// Mark the placeholder's exit channel so anyone holding a stale
		// reference doesn't hang. The placeholder is never returned to
		// callers (AddServer returns before swap), so this is belt-and-
		// braces only.
		select {
		case <-placeholder.exitWaitDone:
		default:
			close(placeholder.exitWaitDone)
		}
	}()

	return placeholder
}

// RemoveServer stops the named server and drops its registry entry.
// Returns nil if the name isn't registered.
func (m *Manager) RemoveServer(name string, stopTimeout time.Duration) error {
	m.mu.Lock()
	srv, ok := m.servers[name]
	delete(m.servers, name)
	m.mu.Unlock()
	if !ok {
		return nil
	}
	return srv.Stop(stopTimeout)
}

// Server looks up a single server by name. Returns nil if unknown.
func (m *Manager) Server(name string) *Server {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.servers[name]
}

// Servers returns all managed servers in name order. The list includes
// failed servers so the UI can render them; callers should filter by
// Status() if they only want Ready rows.
func (m *Manager) Servers() []*Server {
	m.mu.RLock()
	names := make([]string, 0, len(m.servers))
	for n := range m.servers {
		names = append(names, n)
	}
	out := make([]*Server, 0, len(names))
	sort.Strings(names)
	for _, n := range names {
		out = append(out, m.servers[n])
	}
	m.mu.RUnlock()
	return out
}

// Tools returns the union of every Ready server's tool catalog, with
// each tool's name prefixed by "mcp:<server>:" so the agent runner can
// disambiguate when two servers expose tools of the same name. The
// colon separator is illegal in MCP tool names per spec, so collisions
// with builtins are structurally impossible.
type NamespacedTool struct {
	Server string
	Tool   Tool
	// Qualified is the wire-facing name: "mcp:<server>:<tool>".
	Qualified string
}

// Tools returns the aggregated tool catalog across all servers. Empty
// when no server is ready. Order: server name asc, then tool order as
// the server returned them.
func (m *Manager) Tools() []NamespacedTool {
	servers := m.Servers()
	out := []NamespacedTool{}
	for _, s := range servers {
		st, _ := s.Status()
		if st != StatusReady {
			continue
		}
		for _, t := range s.Tools() {
			out = append(out, NamespacedTool{
				Server:    s.Name(),
				Tool:      t,
				Qualified: "mcp:" + s.Name() + ":" + t.Name,
			})
		}
	}
	return out
}

// CallQualified invokes a tool by its qualified "mcp:<server>:<tool>"
// name. Returns an error if the qualified name doesn't parse or the
// named server isn't ready. RPC and tool-level errors flow through
// the underlying *CallToolResult per Client.CallTool semantics.
func (m *Manager) CallQualified(ctx context.Context, qualified string, args json.RawMessage) (*CallToolResult, error) {
	serverName, toolName, ok := ParseQualified(qualified)
	if !ok {
		return nil, fmt.Errorf("not a qualified MCP tool name: %q", qualified)
	}
	srv := m.Server(serverName)
	if srv == nil {
		return nil, fmt.Errorf("unknown MCP server %q", serverName)
	}
	return srv.CallTool(ctx, toolName, []byte(args))
}

// ParseQualified splits "mcp:<server>:<tool>" into its parts. Returns
// ok=false if the input isn't qualified — useful in tool-dispatch code
// that mixes builtins and MCP tools.
func ParseQualified(name string) (server, tool string, ok bool) {
	const prefix = "mcp:"
	if len(name) <= len(prefix) || name[:len(prefix)] != prefix {
		return "", "", false
	}
	rest := name[len(prefix):]
	for i := 0; i < len(rest); i++ {
		if rest[i] == ':' {
			return rest[:i], rest[i+1:], true
		}
	}
	return "", "", false
}

// StopAll stops every server in parallel with the given per-server
// timeout. Blocks until all stops return. Intended for graceful
// shutdown at session/app exit.
func (m *Manager) StopAll(stopTimeout time.Duration) {
	m.mu.Lock()
	servers := make([]*Server, 0, len(m.servers))
	for _, s := range m.servers {
		servers = append(servers, s)
	}
	m.servers = map[string]*Server{}
	m.mu.Unlock()

	var wg sync.WaitGroup
	for _, s := range servers {
		wg.Add(1)
		go func(s *Server) {
			defer wg.Done()
			_ = s.Stop(stopTimeout)
		}(s)
	}
	wg.Wait()
}
