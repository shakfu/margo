package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"sync/atomic"
)

// Client is a thread-safe MCP client speaking JSON-RPC 2.0 over a
// generic stdio-shaped transport (an io.WriteCloser the caller writes
// to and an io.ReadCloser the caller reads from). The intended
// transport is an os/exec subprocess, but tests use io.Pipe pairs so
// no subprocess machinery is required to exercise the protocol.
//
// Lifecycle:
//
//	c := NewClient(name, stdin, stdout, logger)  // starts read loop
//	c.Initialize(ctx)                            // mandatory handshake
//	tools, _ := c.ListTools(ctx)
//	res, _ := c.CallTool(ctx, name, args)
//	c.Close()                                    // tears down read loop
//
// The client is safe for concurrent use after Initialize returns.
// Multiple in-flight calls are correlated by an atomic counter id.
type Client struct {
	name string

	stdin  io.WriteCloser // server-side stdin (we write requests here)
	stdout io.ReadCloser  // server-side stdout (server writes responses here)
	logger *log.Logger

	writeMu sync.Mutex // serialises writes so two goroutines can't interleave a JSON line

	nextID  atomic.Int64
	pending sync.Map // map[int64]chan *Envelope — buffered, size 1

	closed   atomic.Bool
	readDone chan struct{}
	readErr  atomic.Value // error; set when the read loop exits

	serverInfo Info
	serverCaps ServerCapabilities
	negotiated ProtocolVersion

	// protocolVersion is the version the client offers at initialize.
	// Defaults to DefaultProtocolVersion; override via NewClientOptions.
	protocolVersion ProtocolVersion
	clientInfo      Info
}

// Options configures a Client. Zero values fall back to sensible
// defaults (DefaultProtocolVersion, name="margo", a discarding logger).
type Options struct {
	ProtocolVersion ProtocolVersion
	ClientInfo      Info
	Logger          *log.Logger
}

// NewClient wires a Client to the given transport and starts its read
// loop. The caller owns stdin and stdout's lifecycle insofar as Close
// will close both — pass io.NopCloser-wrapped pipes if that's not what
// you want.
func NewClient(name string, stdin io.WriteCloser, stdout io.ReadCloser, opts Options) *Client {
	if opts.ProtocolVersion == "" {
		opts.ProtocolVersion = DefaultProtocolVersion
	}
	if opts.ClientInfo.Name == "" {
		opts.ClientInfo = Info{Name: "margo", Version: "0.1"}
	}
	if opts.Logger == nil {
		opts.Logger = log.New(io.Discard, "", 0)
	}
	c := &Client{
		name:            name,
		stdin:           stdin,
		stdout:          stdout,
		logger:          opts.Logger,
		readDone:        make(chan struct{}),
		protocolVersion: opts.ProtocolVersion,
		clientInfo:      opts.ClientInfo,
	}
	go c.readLoop()
	return c
}

// Name returns the user-facing name this client was constructed with.
// Useful for log lines and error wrapping.
func (c *Client) Name() string { return c.name }

// ServerInfo returns the server's Info as reported in the initialize
// handshake. Zero value before Initialize returns.
func (c *Client) ServerInfo() Info { return c.serverInfo }

// ServerCapabilities returns the server's advertised capabilities. Zero
// value before Initialize returns.
func (c *Client) ServerCapabilities() ServerCapabilities { return c.serverCaps }

// NegotiatedVersion returns the version the server replied with at
// initialize — which may differ from what we offered if the server
// downgraded.
func (c *Client) NegotiatedVersion() ProtocolVersion { return c.negotiated }

// Initialize performs the MCP initialize handshake and follows up with
// the required notifications/initialized notification. Must be called
// exactly once before any tool method.
func (c *Client) Initialize(ctx context.Context) error {
	var result InitializeResult
	err := c.call(ctx, MethodInitialize, InitializeParams{
		ProtocolVersion: c.protocolVersion,
		Capabilities:    ClientCapabilities{},
		ClientInfo:      c.clientInfo,
	}, &result)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	c.serverInfo = result.ServerInfo
	c.serverCaps = result.Capabilities
	c.negotiated = result.ProtocolVersion
	// The notifications/initialized handshake completion is required by
	// spec — servers SHOULD wait for it before accepting other requests.
	if err := c.notify(MethodInitialized, struct{}{}); err != nil {
		return fmt.Errorf("post-initialize notify: %w", err)
	}
	return nil
}

// ListTools fetches the server's tool catalog. Returns an empty slice
// (not nil) when the server advertises no tools — distinguishes
// "no tools today" from "we never asked."
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	var r ListToolsResult
	if err := c.call(ctx, MethodToolsList, struct{}{}, &r); err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}
	if r.Tools == nil {
		return []Tool{}, nil
	}
	return r.Tools, nil
}

// CallTool invokes a named tool with the given arguments. RPC-level
// errors (the request failed) return a Go error. Tool-level errors
// (the tool ran and returned an error) are surfaced via
// CallToolResult.IsError; callers must check both.
func (c *Client) CallTool(ctx context.Context, name string, arguments json.RawMessage) (*CallToolResult, error) {
	var r CallToolResult
	err := c.call(ctx, MethodToolsCall, CallToolParams{Name: name, Arguments: arguments}, &r)
	if err != nil {
		return nil, fmt.Errorf("tools/call %q: %w", name, err)
	}
	return &r, nil
}

// Close shuts the client down: closes stdin (which signals EOF to the
// server's read side), waits for the read loop to drain, then closes
// stdout. Idempotent; safe to call multiple times. Returns the first
// non-nil close error.
func (c *Client) Close() error {
	if c.closed.Swap(true) {
		return nil
	}
	// Closing stdin first signals the server to shut down cleanly. We
	// don't kill the subprocess here — that's the manager's job.
	err1 := c.stdin.Close()
	<-c.readDone
	err2 := c.stdout.Close()

	// Drain pending callers so they don't hang. Each gets an io.EOF.
	c.pending.Range(func(_, v any) bool {
		ch := v.(chan *Envelope)
		select {
		case ch <- &Envelope{Error: &RPCError{Code: ErrCodeInternalError, Message: "client closed"}}:
		default:
		}
		return true
	})

	if err1 != nil {
		return err1
	}
	return err2
}

// readLoop consumes newline-delimited JSON from stdout, decoding each
// frame and dispatching it. Exits when stdout returns EOF or any other
// read error; the error is captured in readErr and surfaced to any
// future caller via a wake-and-fail.
func (c *Client) readLoop() {
	defer close(c.readDone)
	dec := json.NewDecoder(c.stdout)
	for {
		var env Envelope
		if err := dec.Decode(&env); err != nil {
			if err != io.EOF && !c.closed.Load() {
				c.logger.Printf("mcp[%s]: read: %v", c.name, err)
			}
			c.readErr.Store(errOr(err, io.EOF))
			c.drainPending(err)
			return
		}
		c.dispatch(&env)
	}
}

// dispatch routes one decoded envelope. Responses (id present, no
// method) wake the matching pending caller. Notifications (no id) are
// logged and dropped. Server-initiated requests (id + method) are
// answered with method-not-found so the server doesn't hang waiting
// for us to implement sampling/roots/etc.
func (c *Client) dispatch(env *Envelope) {
	hasID := len(env.ID) > 0 && string(env.ID) != "null"
	switch {
	case hasID && env.Method == "":
		// Response to one of our requests.
		var id int64
		if err := json.Unmarshal(env.ID, &id); err != nil {
			c.logger.Printf("mcp[%s]: non-numeric response id %s", c.name, env.ID)
			return
		}
		chV, ok := c.pending.LoadAndDelete(id)
		if !ok {
			c.logger.Printf("mcp[%s]: orphan response id=%d", c.name, id)
			return
		}
		chV.(chan *Envelope) <- env

	case hasID && env.Method != "":
		// Server-initiated request. We don't support any; reply
		// method-not-found so the server can continue.
		c.logger.Printf("mcp[%s]: server request %q — replying method-not-found", c.name, env.Method)
		_ = c.writeEnvelope(Envelope{
			ID:    env.ID,
			Error: &RPCError{Code: ErrCodeMethodNotFound, Message: "method not supported by margo MVP"},
		})

	default:
		// Notification. Log known kinds, ignore unknown.
		switch env.Method {
		case MethodToolsListChanged:
			c.logger.Printf("mcp[%s]: tools list changed (refresh not yet implemented)", c.name)
		default:
			c.logger.Printf("mcp[%s]: notification %q (ignored)", c.name, env.Method)
		}
	}
}

// drainPending wakes every in-flight caller with an error after the
// read loop has died. Without this, a server crash would hang every
// outstanding ListTools / CallTool until the caller's context fired.
func (c *Client) drainPending(cause error) {
	msg := "transport closed"
	if cause != nil && cause != io.EOF {
		msg = "transport error: " + cause.Error()
	}
	c.pending.Range(func(k, v any) bool {
		ch := v.(chan *Envelope)
		select {
		case ch <- &Envelope{Error: &RPCError{Code: ErrCodeInternalError, Message: msg}}:
		default:
		}
		c.pending.Delete(k)
		return true
	})
}

// call sends a request and blocks until either the response arrives or
// ctx fires. Cancellation does NOT cancel the server-side work — MCP
// has no cancel notification in 2025-06-18 spec — but it does free
// the caller; the orphaned response is logged and dropped.
func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	if c.closed.Load() {
		return errors.New("mcp: client closed")
	}

	id := c.nextID.Add(1)
	idRaw, _ := json.Marshal(id)

	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal params: %w", err)
		}
		paramsRaw = b
	}

	ch := make(chan *Envelope, 1)
	c.pending.Store(id, ch)

	if err := c.writeEnvelope(Envelope{
		ID:     idRaw,
		Method: method,
		Params: paramsRaw,
	}); err != nil {
		c.pending.Delete(id)
		return fmt.Errorf("write: %w", err)
	}

	select {
	case <-ctx.Done():
		c.pending.Delete(id)
		return ctx.Err()
	case env := <-ch:
		if env.Error != nil {
			return env.Error
		}
		if result != nil && len(env.Result) > 0 {
			if err := json.Unmarshal(env.Result, result); err != nil {
				return fmt.Errorf("unmarshal result: %w", err)
			}
		}
		return nil
	}
}

// notify sends a request with no id — i.e. a JSON-RPC notification. No
// response is expected; we return after the write completes.
func (c *Client) notify(method string, params any) error {
	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal notify params: %w", err)
		}
		paramsRaw = b
	}
	return c.writeEnvelope(Envelope{Method: method, Params: paramsRaw})
}

// writeEnvelope serialises one envelope as a single JSON line under the
// write mutex. The jsonrpc field is always set to "2.0".
func (c *Client) writeEnvelope(env Envelope) error {
	env.JSONRPC = JSONRPCVersion
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.stdin.Write(data)
	return err
}

func errOr(a, fallback error) error {
	if a != nil {
		return a
	}
	return fallback
}
