package agent

import (
	"context"
	"io"
	"time"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"github.com/shakfu/margo/pkg/margo"
)

// Chat runs a single non-streaming completion through the Eino adapter.
// Useful as a smoke test that the adapter wires correctly.
func Chat(ctx context.Context, c margo.Client, defaults margo.Request, input []*schema.Message) (*schema.Message, error) {
	a := NewAdapter(c, defaults)
	return a.Generate(ctx, input)
}

// ChatStream runs a streaming completion through the Eino adapter and returns
// the concatenated final message. The intermediate stream is consumed
// internally; callers wanting per-chunk delivery should use the Adapter
// directly and read from its StreamReader.
func ChatStream(ctx context.Context, c margo.Client, defaults margo.Request, input []*schema.Message) (*schema.Message, error) {
	a := NewAdapter(c, defaults)
	reader, err := a.Stream(ctx, input)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	var chunks []*schema.Message
	for {
		chunk, err := reader.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, chunk)
	}
	if len(chunks) == 0 {
		return &schema.Message{Role: schema.Assistant}, nil
	}
	if len(chunks) == 1 {
		return chunks[0], nil
	}
	return schema.ConcatMessages(chunks)
}

// React runs a ReAct agent loop: the model is given the supplied tools and
// may call them iteratively before producing a final message. Returns the
// final assistant message.
//
// Provider support: requires a margo.Client whose provider implements tool
// calling. All three first-party providers (anthropic, openai, openrouter)
// qualify.
func React(ctx context.Context, c margo.Client, defaults margo.Request, tools []tool.BaseTool, input []*schema.Message) (*schema.Message, error) {
	adapter := NewAdapter(c, defaults)
	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: adapter,
		ToolsConfig:      compose.ToolsNodeConfig{Tools: tools},
	})
	if err != nil {
		return nil, err
	}
	return agent.Generate(ctx, input)
}

// CurrentTimeTool is a trivial built-in tool that returns the current time
// in RFC3339 format. Useful as a proof-of-life when wiring up a ReAct agent.
func CurrentTimeTool() tool.InvokableTool {
	type args struct {
		// Timezone is an optional IANA timezone name (e.g. "America/Los_Angeles").
		// Defaults to UTC when empty.
		Timezone string `json:"timezone,omitempty" jsonschema:"description=Optional IANA timezone name; defaults to UTC"`
	}
	t, err := toolutils.InferTool(
		"current_time",
		"Returns the current wall-clock time as an RFC3339 string. Call this when the user asks what time it is or needs the current date.",
		func(ctx context.Context, in args) (string, error) {
			loc := time.UTC
			if in.Timezone != "" {
				l, err := time.LoadLocation(in.Timezone)
				if err != nil {
					return "", err
				}
				loc = l
			}
			return time.Now().In(loc).Format(time.RFC3339), nil
		},
	)
	if err != nil {
		// InferTool only fails on bad reflection of the args type, which is a
		// programmer error in this fixed definition — panic at init.
		panic(err)
	}
	return t
}
