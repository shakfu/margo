// Package mcp implements a minimal Model Context Protocol (MCP) client
// for margo. The client speaks MCP over a stdio transport — JSON-RPC 2.0
// messages framed as newline-delimited JSON — which is the dominant
// transport for community MCP servers (filesystem, github, sqlite,
// brave-search, slack, etc.).
//
// Scope is deliberately MVP: tools discovery (tools/list), tool
// invocation (tools/call), and the initialize handshake. Resources,
// prompts, sampling, roots, and notifications are intentionally
// out-of-scope; the boundary in this package is shaped so they can be
// added without breaking the client API.
//
// The package is hand-rolled rather than using a third-party SDK
// (mark3labs/mcp-go and friends) for parity with the rest of margo's
// "we own the wire" pattern: providers, agent adapter, and core all
// hand-roll their protocol layers. The MCP spec is small and stable
// enough that the maintenance cost is modest.
package mcp

import (
	"encoding/json"
	"fmt"
)

// ProtocolVersion is the MCP wire-protocol version negotiated at
// initialize time. Servers SHOULD downgrade gracefully; if they don't,
// callers can pass a different version to NewClient via WithProtocolVersion.
type ProtocolVersion string

// DefaultProtocolVersion is the version margo's client offers at
// initialize. Servers reply with the version they support; the client
// records but does not strictly validate the reply (any 20YY-MM-DD
// version that responds to tools/list and tools/call works for MVP).
const DefaultProtocolVersion ProtocolVersion = "2025-06-18"

// JSONRPCVersion is the only JSON-RPC version MCP uses.
const JSONRPCVersion = "2.0"

// Envelope is the JSON-RPC 2.0 message envelope. Decoded permissively:
// any of {request, response, notification} can be inferred from which
// fields are populated. ID is json.RawMessage so we can support either
// numeric or string ids on the wire (we always emit numeric ourselves).
type Envelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is the JSON-RPC error object. Implements the error interface
// so callers can return it directly from Client methods.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("mcp rpc error %d: %s", e.Code, e.Message)
}

// Standard JSON-RPC error codes used in MCP. The full list is in the
// spec; these are the ones the client emits or recognises.
const (
	ErrCodeParseError     = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternalError  = -32603
)

// Info identifies an MCP client or server (name + version). Surfaces in
// initialize handshake metadata.
type Info struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeParams is the payload of the initialize request. The
// capabilities field advertises what the *client* supports — we leave
// it empty because margo doesn't yet implement sampling, roots, or
// elicitation.
type InitializeParams struct {
	ProtocolVersion ProtocolVersion    `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      Info               `json:"clientInfo"`
}

// ClientCapabilities is an empty struct for MVP. Future fields:
// sampling, roots, elicitation. Kept named so additions are
// backwards-compatible.
type ClientCapabilities struct{}

// InitializeResult is what the server returns from initialize. We
// retain serverInfo + capabilities so the manager can decide whether
// to bother calling tools/list (skip when ServerCapabilities.Tools is nil).
type InitializeResult struct {
	ProtocolVersion ProtocolVersion    `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      Info               `json:"serverInfo"`
}

// ServerCapabilities advertises what the server supports. We only read
// Tools today; the other pointers are kept so future slices can detect
// resources/prompts support without re-decoding the handshake.
type ServerCapabilities struct {
	Tools     *ToolsCapability     `json:"tools,omitempty"`
	Resources *ResourcesCapability `json:"resources,omitempty"`
	Prompts   *PromptsCapability   `json:"prompts,omitempty"`
}

// ToolsCapability flags whether the server emits tools/list_changed
// notifications when its tool catalog mutates. We don't subscribe yet,
// but the field is decoded so the manager can later flip on a refresher.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ResourcesCapability is forward-decoded but unused by the MVP client.
type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

// PromptsCapability is forward-decoded but unused by the MVP client.
type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// Tool is one tool definition returned by tools/list. InputSchema is
// kept as json.RawMessage so we can forward it verbatim to whatever
// schema validator the agent runner uses (eino's tool framework expects
// JSON Schema strings; this preserves field order and arbitrary keys).
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// ListToolsResult is the response shape for tools/list.
type ListToolsResult struct {
	Tools []Tool `json:"tools"`
	// NextCursor is present when the server paginates; we do not paginate
	// in the MVP — community servers rarely have >100 tools — but
	// preserve the field so the manager can later opt in.
	NextCursor string `json:"nextCursor,omitempty"`
}

// CallToolParams is the input to tools/call. Arguments is RawMessage so
// callers (the eino adapter) can forward the model's tool-call payload
// without re-parsing.
type CallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// CallToolResult is the response shape for tools/call. Per spec, IsError
// is a *tool-level* error (the tool ran but returned an error message)
// distinct from an RPC-level error (the request itself failed); the
// client surfaces both — IsError flows through CallToolResult, RPC
// failures return a Go error from Client.CallTool.
type CallToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

// Content is one block in a tool's response. Type is normally "text";
// "image" carries base64 Data + MimeType. The MVP renders text blocks
// verbatim and ignores everything else with a warning.
type Content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// TextContent is a convenience helper for the common case.
func TextContent(s string) Content { return Content{Type: "text", Text: s} }

// MethodInitialize and friends are the method-name string constants the
// client sends. Centralised here so the test suite can refer to them
// without hardcoding strings.
const (
	MethodInitialize         = "initialize"
	MethodInitialized        = "notifications/initialized"
	MethodToolsList          = "tools/list"
	MethodToolsCall          = "tools/call"
	MethodToolsListChanged   = "notifications/tools/list_changed"
)
