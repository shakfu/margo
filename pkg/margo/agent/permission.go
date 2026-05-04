package agent

import (
	"context"
	"errors"

	"github.com/cloudwego/eino/compose"
)

// ReadOnlyTools is the set of tools that don't require a user-approval
// prompt before invocation. The criterion is "no observable side effect
// outside the agent loop" — read-only metadata, lookups, computations.
// Tools that touch the filesystem, network, shell, or any persistent
// state must NOT be added here; the user should see and approve each
// invocation (or pre-authorise via the always-approve list).
var ReadOnlyTools = map[string]bool{
	"current_time": true,
}

// ErrPermissionDenied is returned by the middleware when the user denies a
// tool invocation. Surfaces as a tool_result with isError=true through the
// existing ToolCallbackHandler.OnError path.
var ErrPermissionDenied = errors.New("user denied permission to invoke tool")

// PermissionGate is consulted before each invocation of a non-read-only
// tool. Returning (true, _) allows the call to proceed. Returning
// (false, nil) denies and surfaces ErrPermissionDenied. The bool flows up
// from the user's decision.
//
// Implementations typically:
//  1. Emit a step event to the UI carrying the tool name + arguments.
//  2. Block on a channel keyed by a request id until the user clicks
//     Approve / Deny / Always Approve in the composer.
//  3. Persist "Always" approvals so subsequent calls in the same run skip
//     the gate.
//
// Must respect ctx — return promptly when the parent stream is cancelled
// so the React loop can unwind.
type PermissionGate func(ctx context.Context, toolName, arguments string) (approved bool, err error)

// permissionMiddleware wraps each invokable tool call in a permission
// check. Read-only tools (per ReadOnlyTools) skip the gate. When the gate
// is nil, every call proceeds — used by tests and by entrypoints that
// don't have a UI to prompt.
func permissionMiddleware(gate PermissionGate) compose.ToolMiddleware {
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, in *compose.ToolInput) (*compose.ToolOutput, error) {
				if gate == nil || (in != nil && ReadOnlyTools[in.Name]) {
					return next(ctx, in)
				}
				name, args := "", ""
				if in != nil {
					name = in.Name
					args = in.Arguments
				}
				ok, err := gate(ctx, name, args)
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, ErrPermissionDenied
				}
				return next(ctx, in)
			}
		},
		Streamable: func(next compose.StreamableToolEndpoint) compose.StreamableToolEndpoint {
			return func(ctx context.Context, in *compose.ToolInput) (*compose.StreamToolOutput, error) {
				if gate == nil || (in != nil && ReadOnlyTools[in.Name]) {
					return next(ctx, in)
				}
				name, args := "", ""
				if in != nil {
					name = in.Name
					args = in.Arguments
				}
				ok, err := gate(ctx, name, args)
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, ErrPermissionDenied
				}
				return next(ctx, in)
			}
		},
	}
}

