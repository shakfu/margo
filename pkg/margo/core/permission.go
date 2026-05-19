package core

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// PermissionDecision is the user's response to a tool-permission prompt.
// Always is honored only when Approved is true; on Deny it is ignored.
type PermissionDecision struct {
	Approved bool
	Always   bool
}

// PermissionBroker brokers per-tool-call approval prompts between an
// in-flight agent run and whichever frontend is hosting the user. The
// runner asks New() for an id + channel, the session emits a Permission
// event carrying that id, and the frontend later calls Respond(id, …) to
// resolve the channel.
//
// Decoupling the broker from the transport lets a Wails frontend render
// a modal, a TUI render an inline confirm, and an HTTP server pause an
// SSE stream — all against the same channel-based primitive.
type PermissionBroker struct {
	pending sync.Map // map[string]chan PermissionDecision
	counter uint64
}

// NewPermissionBroker returns a ready-to-use broker.
func NewPermissionBroker() *PermissionBroker {
	return &PermissionBroker{}
}

// New mints a fresh permission id and returns it alongside a buffered
// channel the caller can block on. The runner is expected to defer-delete
// via Cancel(id) when its parent context fires; Respond(id, …) also
// cleans up.
func (b *PermissionBroker) New() (id string, ch chan PermissionDecision) {
	n := atomic.AddUint64(&b.counter, 1)
	id = fmt.Sprintf("perm-%d-%d", time.Now().UnixNano(), n)
	ch = make(chan PermissionDecision, 1)
	b.pending.Store(id, ch)
	return id, ch
}

// Cancel drops the pending entry without delivering a decision. Safe to
// call from a runner's defer even when Respond already fired (no-op).
func (b *PermissionBroker) Cancel(id string) {
	b.pending.Delete(id)
}

// Respond delivers the user's decision to whichever runner is waiting on
// the given id. Returns an error if the id is unknown — that means the
// run was cancelled or the response is a duplicate.
func (b *PermissionBroker) Respond(id string, decision PermissionDecision) error {
	v, ok := b.pending.LoadAndDelete(id)
	if !ok {
		return fmt.Errorf("unknown permission id %q (already responded or run cancelled)", id)
	}
	v.(chan PermissionDecision) <- decision
	return nil
}

// gate adapts the broker to the agent.RunByType permission callback
// shape. It augments the supplied approvedSet on an Always-Approve so
// later same-tool calls in the same run skip the prompt.
func (b *PermissionBroker) gate(emit func(id, name, args string), approvedSet map[string]bool, approvedMu *sync.Mutex) func(context.Context, string, string) (bool, error) {
	return func(gctx context.Context, name, args string) (bool, error) {
		approvedMu.Lock()
		alreadyApproved := approvedSet[name]
		approvedMu.Unlock()
		if alreadyApproved {
			return true, nil
		}
		id, ch := b.New()
		defer b.Cancel(id)
		emit(id, name, args)
		select {
		case <-gctx.Done():
			return false, gctx.Err()
		case d := <-ch:
			if d.Always && d.Approved {
				approvedMu.Lock()
				approvedSet[name] = true
				approvedMu.Unlock()
			}
			return d.Approved, nil
		}
	}
}
