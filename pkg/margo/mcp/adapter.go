package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
)

// AsEinoTool wraps a single MCP tool exposed by a managed Server into
// the eino tool.InvokableTool interface so the agent runner can equip
// it alongside builtins. The wrapper preserves the namespaced
// "mcp:<server>:<tool>" name; collisions with builtins are
// structurally impossible because `:` is illegal in MCP tool names.
//
// Schema flow: MCP's inputSchema is JSON Schema 2020-12 carried as
// json.RawMessage on the wire. We unmarshal into eino-contrib's
// jsonschema.Schema (which speaks the same dialect) and pass through
// schema.NewParamsOneOfByJSONSchema so the chat model receives the
// server's schema verbatim. Servers with malformed or absent schemas
// fall back to a no-args tool — the agent can still discover and
// surface them; the user just won't get parameter completion.
func AsEinoTool(mgr *Manager, nt NamespacedTool) tool.InvokableTool {
	return &mcpToolAdapter{manager: mgr, nt: nt}
}

// ManagerTools wraps every Ready server's tools as eino tools in one
// call. Convenient for the agent runner's tool-equip step.
func ManagerTools(mgr *Manager) []tool.BaseTool {
	nts := mgr.Tools()
	out := make([]tool.BaseTool, 0, len(nts))
	for _, nt := range nts {
		out = append(out, AsEinoTool(mgr, nt))
	}
	return out
}

type mcpToolAdapter struct {
	manager *Manager
	nt      NamespacedTool
}

// Info builds the schema.ToolInfo the chat model uses to decide
// when/how to call this tool. The MCP server's description is verbatim;
// when missing, we synthesise "MCP tool: <server>/<name>" so the
// model has something better than an empty string.
func (a *mcpToolAdapter) Info(ctx context.Context) (*schema.ToolInfo, error) {
	desc := strings.TrimSpace(a.nt.Tool.Description)
	if desc == "" {
		desc = fmt.Sprintf("MCP tool: %s/%s", a.nt.Server, a.nt.Tool.Name)
	}
	info := &schema.ToolInfo{
		Name: a.nt.Qualified,
		Desc: desc,
	}
	if len(a.nt.Tool.InputSchema) > 0 {
		var sch jsonschema.Schema
		if err := json.Unmarshal(a.nt.Tool.InputSchema, &sch); err != nil {
			// Bad schema is not fatal — log inside the manager and
			// surface the tool as no-args. The model will likely refuse
			// to call it, which is the right outcome.
			return info, nil
		}
		info.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(&sch)
	}
	return info, nil
}

// InvokableRun forwards the model's JSON-encoded arguments to the
// underlying server via Manager.CallQualified and flattens the
// CallToolResult.Content blocks into a single string the chat model
// can ingest as a tool message.
//
// Error handling has two layers:
//   - RPC errors (transport / protocol failures) → returned as a Go
//     error so the agent runner surfaces them as StepError.
//   - Tool-level errors (CallToolResult.IsError = true) → returned as a
//     string prefixed "ERROR:" so the model sees the failure and can
//     react. This matches MCP semantics: tool errors are recoverable;
//     RPC errors are not.
func (a *mcpToolAdapter) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	args := json.RawMessage(argumentsInJSON)
	if argumentsInJSON == "" {
		args = nil
	}
	res, err := a.manager.CallQualified(ctx, a.nt.Qualified, args)
	if err != nil {
		return "", err
	}
	out := flattenContent(res.Content)
	if res.IsError {
		// Prefix so the model can recognise. The full content is still
		// included so the model has the server's error message.
		return "ERROR: " + out, nil
	}
	return out, nil
}

// flattenContent concatenates an MCP tool's content blocks into a
// single string. Text blocks are emitted verbatim; image and other
// binary blocks become a "[image: <mime>, <bytes>]" marker so the
// model knows something was elided (vs. silent loss).
//
// Multi-block responses get blank-line separators so the model can
// still parse paragraph structure when a server returns several text
// blocks (some servers split their output for streaming progress).
func flattenContent(blocks []Content) string {
	if len(blocks) == 0 {
		return ""
	}
	var parts []string
	for _, c := range blocks {
		switch c.Type {
		case "text", "":
			if c.Text != "" {
				parts = append(parts, c.Text)
			}
		case "image":
			mime := c.MimeType
			if mime == "" {
				mime = "image/*"
			}
			// We can't render binary in the chat model's text-only
			// tool-result channel; mark its presence.
			parts = append(parts, fmt.Sprintf("[image: %s, %d bytes]", mime, len(c.Data)))
		default:
			parts = append(parts, fmt.Sprintf("[unsupported content type: %s]", c.Type))
		}
	}
	return strings.Join(parts, "\n\n")
}
