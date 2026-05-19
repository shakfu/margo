package core

import (
	"context"
	"sort"

	"github.com/cloudwego/eino/components/tool"

	"github.com/shakfu/margo/pkg/margo/agent"
)

// toolCtor builds a tool for a given run. The Session argument lets a
// tool close over per-run state (active workspace, RAG indexer, …) that
// is only known at run time rather than package-init time.
type toolCtor func(*Session) tool.BaseTool

// builtinTools is the registry of tools the agent path can equip.
// Entries gated on an availability predicate (e.g. quarto_render) are
// only registered when the underlying binary is on PATH at process start.
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

// Tools returns the names of every registered tool.
func (s *Session) Tools() []string {
	out := make([]string, 0, len(builtinTools))
	for name := range builtinTools {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ToolsMetadata returns one ToolMetadata per registered tool, sorted by
// name for deterministic UI rendering.
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
	return out
}

// buildTools resolves a name list to constructed tools. Returns the first
// unknown name as an error so the caller can surface a clear message.
func (s *Session) buildTools(names []string) ([]tool.BaseTool, error) {
	tools := make([]tool.BaseTool, 0, len(names))
	for _, n := range names {
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
