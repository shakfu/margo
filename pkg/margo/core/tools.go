package core

import (
	"context"
	"sort"
	"strings"

	"github.com/cloudwego/eino/components/tool"

	"github.com/shakfu/margo/pkg/margo/agent"
	"github.com/shakfu/margo/pkg/margo/mcp"
)

// toolCtor builds a tool for a given run. The Session argument lets a
// tool close over per-run state (active workspace, RAG indexer, …) that
// is only known at run time rather than package-init time.
type toolCtor func(*Session) tool.BaseTool

// builtinTools is the registry of tools the agent path can equip.
// Entries gated on an availability predicate (e.g. quarto_render) are
// only registered when the underlying binary is on PATH at process start.
//
// MCP tools are NOT in this map — they live in s.mcp and are merged on
// every Tools/ToolsMetadata/buildTools call so a server that becomes
// ready after Session construction is picked up without re-registering.
var builtinTools = func() map[string]toolCtor {
	m := map[string]toolCtor{
		"current_time":     func(*Session) tool.BaseTool { return agent.CurrentTimeTool() },
		"web_fetch":        func(*Session) tool.BaseTool { return agent.WebFetchTool() },
		"search_knowledge": func(s *Session) tool.BaseTool { return agent.SearchKnowledgeTool(s.workspaces.ActiveIndexer()) },
	}
	if agent.QuartoAvailable() {
		dir, _ := agent.DefaultOutputDir()
		m["quarto_render"] = func(*Session) tool.BaseTool {
			return agent.QuartoRenderTool(dir)
		}
	}
	return m
}()

// Tools returns the names of every available tool, builtins first
// (sorted), then any MCP tools currently exposed by Ready servers
// (in mcp:server:tool form, sorted within their namespace).
//
// The split-then-merge order makes the front-end's tool picker stable:
// rebuilding the list mid-session can only add MCP entries at the end;
// builtins never reshuffle.
func (s *Session) Tools() []string {
	out := make([]string, 0, len(builtinTools))
	for name := range builtinTools {
		out = append(out, name)
	}
	sort.Strings(out)
	for _, nt := range s.mcp.Tools() {
		out = append(out, nt.Qualified)
	}
	return out
}

// ToolsMetadata returns one ToolMetadata per available tool. Builtins
// expose their `agent.ReadOnlyTools` flag; MCP tools default to
// non-read-only because the server author can't be trusted to mark
// themselves safe — users grant per-tool always-approve through the
// existing permission flow if they want lower friction.
func (s *Session) ToolsMetadata(ctx context.Context) []ToolMetadata {
	out := make([]ToolMetadata, 0, len(builtinTools))
	for name, ctor := range builtinTools {
		t := ctor(s)
		desc := ""
		if info, err := t.Info(ctx); err == nil && info != nil {
			desc = info.Desc
		}
		_, streamable := t.(tool.StreamableTool)
		out = append(out, ToolMetadata{
			Name:         name,
			Description:  desc,
			IsReadOnly:   agent.ReadOnlyTools[name],
			IsStreamable: streamable,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	// MCP tools appended after builtins; their Info comes off the wire
	// (description set by the server). Streamable=false because MCP's
	// tools/call returns a single CallToolResult in 2025-06-18 — no
	// per-tool streaming. Read-only=false by policy.
	mcpEntries := make([]ToolMetadata, 0)
	for _, nt := range s.mcp.Tools() {
		desc := strings.TrimSpace(nt.Tool.Description)
		mcpEntries = append(mcpEntries, ToolMetadata{
			Name:         nt.Qualified,
			Description:  desc,
			IsReadOnly:   false,
			IsStreamable: false,
		})
	}
	sort.Slice(mcpEntries, func(i, j int) bool { return mcpEntries[i].Name < mcpEntries[j].Name })
	return append(out, mcpEntries...)
}

// buildTools resolves a name list to constructed tools. Names with the
// "mcp:" prefix are resolved against the MCP manager; everything else
// must be a builtin. Returns the first unknown name as an error so the
// caller can surface a clear message.
//
// Unknown MCP tool names (server doesn't exist or hasn't reached
// Ready) error here rather than at first call so the agent runner
// can surface "you asked for an MCP tool that isn't available" once,
// up-front, rather than letting the model burn turns invoking a
// tool that will always fail.
func (s *Session) buildTools(names []string) ([]tool.BaseTool, error) {
	tools := make([]tool.BaseTool, 0, len(names))
	// Cache the manager's tool list once so we don't iterate per name.
	mcpByQualified := map[string]mcp.NamespacedTool{}
	for _, nt := range s.mcp.Tools() {
		mcpByQualified[nt.Qualified] = nt
	}
	for _, n := range names {
		if strings.HasPrefix(n, "mcp:") {
			nt, ok := mcpByQualified[n]
			if !ok {
				return nil, unknownToolError(n)
			}
			tools = append(tools, mcp.AsEinoTool(s.mcp, nt))
			continue
		}
		ctor, ok := builtinTools[n]
		if !ok {
			return nil, unknownToolError(n)
		}
		tools = append(tools, ctor(s))
	}
	return tools, nil
}

type unknownToolError string

func (e unknownToolError) Error() string { return "unknown tool: " + string(e) }
