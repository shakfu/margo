package core

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// settleTimeout bounds every wait in this file. It exists to turn a
// deadlock into a readable failure, not to assert how fast anything is,
// so it is set far above any plausible scheduling delay: `go test -race
// ./...` runs ten packages at once and a two-second budget flaked.
const settleTimeout = 30 * time.Second

func TestPermissionBrokerRespondDeliversDecision(t *testing.T) {
	b := NewPermissionBroker()
	id, ch := b.New()
	if id == "" {
		t.Fatal("New returned an empty id")
	}

	if err := b.Respond(id, PermissionDecision{Approved: true}); err != nil {
		t.Fatalf("Respond: %v", err)
	}
	select {
	case d := <-ch:
		if !d.Approved {
			t.Error("decision did not carry Approved")
		}
	case <-time.After(settleTimeout):
		t.Fatal("decision never arrived on the channel")
	}
}

// A second Respond for the same id must fail rather than block forever
// on a channel nobody is reading — the id is deleted on first delivery.
func TestPermissionBrokerRespondTwiceErrors(t *testing.T) {
	b := NewPermissionBroker()
	id, ch := b.New()
	if err := b.Respond(id, PermissionDecision{Approved: true}); err != nil {
		t.Fatalf("first Respond: %v", err)
	}
	<-ch
	err := b.Respond(id, PermissionDecision{Approved: false})
	if err == nil {
		t.Fatal("second Respond succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "unknown permission id") {
		t.Errorf("got %v, want an unknown-id error", err)
	}
}

func TestPermissionBrokerRespondUnknownID(t *testing.T) {
	b := NewPermissionBroker()
	if err := b.Respond("perm-does-not-exist", PermissionDecision{Approved: true}); err == nil {
		t.Fatal("expected an error for an unknown id")
	}
}

// Cancel drops the pending slot; a later Respond must not block.
func TestPermissionBrokerCancelThenRespond(t *testing.T) {
	b := NewPermissionBroker()
	id, _ := b.New()
	b.Cancel(id)
	if err := b.Respond(id, PermissionDecision{Approved: true}); err == nil {
		t.Fatal("Respond after Cancel succeeded, want an error")
	}
}

func TestPermissionBrokerIDsAreUnique(t *testing.T) {
	b := NewPermissionBroker()
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id, _ := b.New()
		if seen[id] {
			t.Fatalf("duplicate permission id %q", id)
		}
		seen[id] = true
	}
}

// gate is the piece the whole permission story rests on. These tests
// drive it the way Session.StreamAgent does: emit fires on a worker
// goroutine, the user answers later.

func newTestGate(b *PermissionBroker, approved map[string]bool) (gate func(context.Context, string, string) (bool, error), emitted chan string) {
	emitted = make(chan string, 8)
	var mu sync.Mutex
	g := b.gate(func(id, _, _ string) { emitted <- id }, approved, &mu)
	return g, emitted
}

func TestGateApprovesOnUserDecision(t *testing.T) {
	b := NewPermissionBroker()
	gate, emitted := newTestGate(b, map[string]bool{})

	var ok bool
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		ok, err = gate(context.Background(), "web_fetch", `{"url":"https://example.com"}`)
	}()

	id := <-emitted
	if err := b.Respond(id, PermissionDecision{Approved: true}); err != nil {
		t.Fatalf("Respond: %v", err)
	}
	<-done

	if err != nil {
		t.Fatalf("gate returned err: %v", err)
	}
	if !ok {
		t.Error("gate denied an approved call")
	}
}

func TestGateDeniesOnUserDecision(t *testing.T) {
	b := NewPermissionBroker()
	gate, emitted := newTestGate(b, map[string]bool{})

	var ok bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		ok, _ = gate(context.Background(), "web_fetch", "{}")
	}()

	id := <-emitted
	if err := b.Respond(id, PermissionDecision{Approved: false}); err != nil {
		t.Fatalf("Respond: %v", err)
	}
	<-done

	if ok {
		t.Error("gate approved a denied call")
	}
}

// A pre-approved tool must not prompt at all — no emit, no broker entry.
func TestGateSkipsPromptForPreApproved(t *testing.T) {
	b := NewPermissionBroker()
	gate, emitted := newTestGate(b, map[string]bool{"web_fetch": true})

	ok, err := gate(context.Background(), "web_fetch", "{}")
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if !ok {
		t.Error("pre-approved tool was denied")
	}
	select {
	case id := <-emitted:
		t.Fatalf("pre-approved tool still prompted (id %s)", id)
	default:
	}
}

// "Always" promotes the tool into the run's approved set, so the next
// call of the same tool goes straight through.
func TestGateAlwaysPromotesForEligibleTool(t *testing.T) {
	b := NewPermissionBroker()
	approved := map[string]bool{}
	gate, emitted := newTestGate(b, approved)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = gate(context.Background(), "web_fetch", "{}")
	}()
	id := <-emitted
	if err := b.Respond(id, PermissionDecision{Approved: true, Always: true}); err != nil {
		t.Fatalf("Respond: %v", err)
	}
	<-done

	// Second call must not prompt.
	ok, err := gate(context.Background(), "web_fetch", "{}")
	if err != nil {
		t.Fatalf("second gate call: %v", err)
	}
	if !ok {
		t.Error("second call denied after Always")
	}
	select {
	case <-emitted:
		t.Fatal("second call prompted despite Always")
	default:
	}
}

// The §4.2 rule: quarto_render is in agent.NoAlwaysApproveTools, so
// "Always" approves this one call and nothing more.
func TestGateAlwaysIgnoredForIneligibleTool(t *testing.T) {
	b := NewPermissionBroker()
	gate, emitted := newTestGate(b, map[string]bool{})

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = gate(context.Background(), "quarto_render", "{}")
	}()
	id := <-emitted
	if err := b.Respond(id, PermissionDecision{Approved: true, Always: true}); err != nil {
		t.Fatalf("Respond: %v", err)
	}
	<-done

	// The second call must prompt again.
	go func() {
		_, _ = gate(context.Background(), "quarto_render", "{}")
	}()
	select {
	case id2 := <-emitted:
		_ = b.Respond(id2, PermissionDecision{Approved: false})
	case <-time.After(settleTimeout):
		t.Fatal("second quarto_render call did not prompt; Always was honoured")
	}
}

// A cancelled run must unblock the gate rather than stranding the
// agent loop on a prompt nobody will answer.
func TestGateReturnsOnContextCancel(t *testing.T) {
	b := NewPermissionBroker()
	gate, emitted := newTestGate(b, map[string]bool{})

	ctx, cancel := context.WithCancel(context.Background())
	var ok bool
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		ok, err = gate(ctx, "web_fetch", "{}")
	}()

	<-emitted
	cancel()

	select {
	case <-done:
	case <-time.After(settleTimeout):
		t.Fatal("gate did not return after context cancellation")
	}
	if ok {
		t.Error("gate approved after cancellation")
	}
	if err == nil {
		t.Error("gate swallowed the cancellation error")
	}
}

// Concurrency net. Run with -race: the broker's sync.Map and the
// caller-supplied approval mutex are the parts most likely to be wrong.
func TestBrokerConcurrentNewAndRespond(t *testing.T) {
	b := NewPermissionBroker()
	const n = 64

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, ch := b.New()
			go func() { _ = b.Respond(id, PermissionDecision{Approved: true}) }()
			select {
			case <-ch:
			case <-time.After(settleTimeout):
				t.Error("decision never arrived")
			}
		}()
	}
	wg.Wait()
}

func TestGateConcurrentAlwaysPromotion(t *testing.T) {
	b := NewPermissionBroker()
	approved := map[string]bool{}
	var mu sync.Mutex
	emitted := make(chan string, 64)
	gate := b.gate(func(id, _, _ string) { emitted <- id }, approved, &mu)

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = gate(context.Background(), "web_fetch", "{}")
		}()
	}
	// Answer every prompt that appears until all callers have returned.
	answered := make(chan struct{})
	go func() {
		defer close(answered)
		for id := range emitted {
			_ = b.Respond(id, PermissionDecision{Approved: true, Always: true})
		}
	}()
	wg.Wait()
	close(emitted)
	<-answered
}
