package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shakfu/margo/pkg/margo"
)

// scriptedClient is a margo.Client that replays a fixed chunk sequence.
// Session only needs Name/Complete/Stream, so a fake keeps these tests
// off the network entirely.
type scriptedClient struct {
	name   string
	chunks []margo.Chunk
	resp   margo.Response
	err    error

	// blockUntil, when non-nil, holds Stream's producer goroutine open
	// after the first chunk so cancellation has something to interrupt.
	blockUntil chan struct{}
}

func (c *scriptedClient) Name() string { return c.name }

func (c *scriptedClient) Complete(_ context.Context, _ margo.Request) (margo.Response, error) {
	if c.err != nil {
		return margo.Response{}, c.err
	}
	return c.resp, nil
}

func (c *scriptedClient) Stream(ctx context.Context, _ margo.Request) (<-chan margo.Chunk, error) {
	if c.err != nil {
		return nil, c.err
	}
	out := make(chan margo.Chunk, len(c.chunks)+1)
	go func() {
		defer close(out)
		for _, ch := range c.chunks {
			select {
			case out <- ch:
			case <-ctx.Done():
				return
			}
		}
		if c.blockUntil != nil {
			select {
			case <-c.blockUntil:
			case <-ctx.Done():
			}
		}
	}()
	return out, nil
}

// newTestSession builds a Session with fake clients installed directly.
// NewSession would require real API keys to populate them, and the
// point here is the orchestration, not provider construction.
func newTestSession(t *testing.T, clients map[string]*scriptedClient) *Session {
	t.Helper()
	s := NewSession(Config{AttachmentRoot: t.TempDir(), CatalogDir: t.TempDir()})
	if c, ok := clients["anthropic"]; ok {
		s.anthropic = c
	}
	if c, ok := clients["openai"]; ok {
		s.openai = c
	}
	if c, ok := clients["openrouter"]; ok {
		s.openrouter = c
	}
	return s
}

func TestProvidersListsOnlyConfigured(t *testing.T) {
	s := newTestSession(t, map[string]*scriptedClient{
		"anthropic": {name: "anthropic"},
	})
	got := s.Providers()
	if len(got) != 1 || got[0] != "anthropic" {
		t.Fatalf("Providers() = %v, want [anthropic]", got)
	}
}

func TestClientForUnknownProvider(t *testing.T) {
	s := newTestSession(t, nil)
	_, err := s.clientFor("gemini")
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("got %v, want an unknown-provider error", err)
	}
}

// The "no API key" path has to be distinguishable from "no such
// provider" — they lead the user to different fixes.
func TestClientForUnconfiguredProvider(t *testing.T) {
	s := newTestSession(t, nil)
	_, err := s.clientFor("openai")
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("got %v, want a not-configured error", err)
	}
}

func TestChatMapsResponse(t *testing.T) {
	s := newTestSession(t, map[string]*scriptedClient{
		"anthropic": {name: "anthropic", resp: margo.Response{
			Text:     "hi",
			Thinking: "hmm",
			Model:    "claude-haiku-4-5",
			Usage:    margo.Usage{InputTokens: 3, OutputTokens: 4},
		}},
	})
	got, err := s.Chat(context.Background(), ChatRequest{Provider: "anthropic"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got.Text != "hi" || got.Thinking != "hmm" || got.Model != "claude-haiku-4-5" {
		t.Errorf("unexpected response: %+v", got)
	}
	if got.Usage.InputTokens != 3 || got.Usage.OutputTokens != 4 {
		t.Errorf("usage not carried through: %+v", got.Usage)
	}
}

func TestStreamEmitsTextThenDoneWithUsage(t *testing.T) {
	s := newTestSession(t, map[string]*scriptedClient{
		"openai": {name: "openai", chunks: []margo.Chunk{
			{Kind: margo.ChunkText, Text: "he"},
			{Kind: margo.ChunkThinking, Text: "reasoning"},
			{Kind: margo.ChunkText, Text: "llo"},
			{Usage: &margo.Usage{InputTokens: 7, OutputTokens: 9, TotalMs: 12}},
		}},
	})

	ch, err := s.Stream(context.Background(), "run-1", ChatRequest{Provider: "openai"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var kinds []EventKind
	var text strings.Builder
	var done *Event
	for ev := range ch {
		kinds = append(kinds, ev.Kind)
		switch ev.Kind {
		case EventText:
			text.WriteString(ev.Text)
		case EventDone:
			e := ev
			done = &e
		}
	}

	if text.String() != "hello" {
		t.Errorf("text = %q, want %q", text.String(), "hello")
	}
	want := []EventKind{EventText, EventThinking, EventText, EventDone}
	if len(kinds) != len(want) {
		t.Fatalf("event kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("event kinds = %v, want %v", kinds, want)
		}
	}
	if done == nil || done.Usage == nil {
		t.Fatal("Done event carried no usage")
	}
	if done.Usage.InputTokens != 7 || done.Usage.OutputTokens != 9 || done.Usage.TotalMs != 12 {
		t.Errorf("usage = %+v", done.Usage)
	}
}

// A mid-stream provider error terminates the run; no Done follows.
func TestStreamSurfacesMidStreamError(t *testing.T) {
	boom := errors.New("upstream exploded")
	s := newTestSession(t, map[string]*scriptedClient{
		"openai": {name: "openai", chunks: []margo.Chunk{
			{Kind: margo.ChunkText, Text: "partial"},
			{Err: boom},
			{Kind: margo.ChunkText, Text: "never seen"},
		}},
	})

	ch, err := s.Stream(context.Background(), "run-err", ChatRequest{Provider: "openai"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var last Event
	n := 0
	for ev := range ch {
		last = ev
		n++
	}
	if n != 2 {
		t.Fatalf("got %d events, want 2 (text then error)", n)
	}
	if last.Kind != EventError {
		t.Fatalf("final event kind = %s, want error", last.Kind)
	}
	if !errors.Is(last.Err, boom) {
		t.Errorf("error not propagated: %v", last.Err)
	}
}

// Stream registers its id up front, so a second run under the same id
// must be refused rather than silently clobbering the first's cancel.
func TestStreamRejectsDuplicateID(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	s := newTestSession(t, map[string]*scriptedClient{
		"openai": {name: "openai", blockUntil: block, chunks: []margo.Chunk{
			{Kind: margo.ChunkText, Text: "x"},
		}},
	})

	if _, err := s.Stream(context.Background(), "dup", ChatRequest{Provider: "openai"}); err != nil {
		t.Fatalf("first Stream: %v", err)
	}
	_, err := s.Stream(context.Background(), "dup", ChatRequest{Provider: "openai"})
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("got %v, want an already-in-use error", err)
	}
}

// Cancel must end an in-flight run and free the id for reuse.
func TestCancelEndsStreamAndReleasesID(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	s := newTestSession(t, map[string]*scriptedClient{
		"openai": {name: "openai", blockUntil: block, chunks: []margo.Chunk{
			{Kind: margo.ChunkText, Text: "x"},
		}},
	})

	ch, err := s.Stream(context.Background(), "cancel-me", ChatRequest{Provider: "openai"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if ev := <-ch; ev.Kind != EventText {
		t.Fatalf("first event = %s, want text", ev.Kind)
	}

	s.Cancel("cancel-me")

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range ch {
		}
	}()
	select {
	case <-drained:
	case <-time.After(settleTimeout):
		t.Fatal("stream did not close after Cancel")
	}

	// The id is free again.
	if _, err := s.Stream(context.Background(), "cancel-me", ChatRequest{Provider: "openai"}); err != nil {
		t.Fatalf("id was not released after Cancel: %v", err)
	}
	s.Cancel("cancel-me")
}

func TestCancelUnknownIDIsNoOp(t *testing.T) {
	s := newTestSession(t, nil)
	s.Cancel("never-registered") // must not panic
}

// The registry is touched from the caller's goroutine and from each
// run's worker. Run with -race.
func TestCancelRegistryConcurrentAccess(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	s := newTestSession(t, map[string]*scriptedClient{
		"openai": {name: "openai", blockUntil: block},
	})

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := string(rune('a'+i%26)) + "-run"
			ch, err := s.Stream(context.Background(), id, ChatRequest{Provider: "openai"})
			if err != nil {
				return // duplicate id, expected under contention
			}
			s.Cancel(id)
			for range ch {
			}
		}(i)
	}
	wg.Wait()
}

func TestStreamErrorsBeforeRegisteringUnknownProvider(t *testing.T) {
	s := newTestSession(t, nil)
	if _, err := s.Stream(context.Background(), "x", ChatRequest{Provider: "nope"}); err == nil {
		t.Fatal("expected an error for an unknown provider")
	}
	// The failed run must not have consumed the id.
	s.openai = &scriptedClient{name: "openai"}
	if _, err := s.Stream(context.Background(), "x", ChatRequest{Provider: "openai"}); err != nil {
		t.Fatalf("id was leaked by the failed run: %v", err)
	}
}

func TestBuildToolsRejectsUnknownBuiltin(t *testing.T) {
	s := newTestSession(t, nil)
	_, err := s.buildTools([]string{"current_time", "does_not_exist"})
	if err == nil || !strings.Contains(err.Error(), "unknown tool: does_not_exist") {
		t.Fatalf("got %v, want an unknown-tool error", err)
	}
}

// An MCP name that no ready server exposes has to fail here, not on the
// model's first call to it.
func TestBuildToolsRejectsUnknownMCPTool(t *testing.T) {
	s := newTestSession(t, nil)
	_, err := s.buildTools([]string{"mcp:ghost:read_file"})
	if err == nil || !strings.Contains(err.Error(), "unknown tool: mcp:ghost:read_file") {
		t.Fatalf("got %v, want an unknown-tool error", err)
	}
}

func TestBuildToolsResolvesBuiltins(t *testing.T) {
	s := newTestSession(t, nil)
	tools, err := s.buildTools([]string{"current_time", "web_fetch"})
	if err != nil {
		t.Fatalf("buildTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}
}

func TestToolsMetadataFlags(t *testing.T) {
	s := newTestSession(t, nil)
	byName := map[string]ToolMetadata{}
	for _, m := range s.ToolsMetadata(context.Background()) {
		byName[m.Name] = m
	}

	ct, ok := byName["current_time"]
	if !ok {
		t.Fatal("current_time missing from the catalog")
	}
	if !ct.IsReadOnly {
		t.Error("current_time should be read-only")
	}
	if !ct.AllowsAlways {
		t.Error("current_time should be eligible for Always")
	}

	wf, ok := byName["web_fetch"]
	if !ok {
		t.Fatal("web_fetch missing from the catalog")
	}
	if wf.IsReadOnly {
		t.Error("web_fetch must not be read-only; it reaches the network")
	}
	if !wf.IsStreamable {
		t.Error("web_fetch should report as streamable")
	}

	// quarto_render only registers when the binary is present.
	if q, ok := byName["quarto_render"]; ok && q.AllowsAlways {
		t.Error("quarto_render must not be eligible for Always")
	}
}

func TestToolsIsSorted(t *testing.T) {
	s := newTestSession(t, nil)
	got := s.Tools()
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("Tools() not sorted: %v", got)
		}
	}
}

// A stale grant for a tool that has since become Always-ineligible must
// be dropped, so the run prompts instead of silently proceeding.
func TestStreamAgentDropsIneligibleAutoApprovals(t *testing.T) {
	s := newTestSession(t, map[string]*scriptedClient{
		"openai": {name: "openai"},
	})
	req := AgentRequest{
		ChatRequest: ChatRequest{Provider: "openai"},
		ToolNames:   []string{"current_time"},
		AutoApprove: []string{"web_fetch", "quarto_render"},
	}
	ch, err := s.StreamAgent(context.Background(), "agent-run", req)
	if err != nil {
		t.Fatalf("StreamAgent: %v", err)
	}
	for range ch { // drain; the fake client ends the run immediately
	}
	// The filtering itself is asserted through the gate in
	// permission_test.go; this test guards the wiring — a rejected
	// name must not abort the run.
}

// These four are the regression net for a wiring bug that shipped past
// the whole suite: NewSession built its Session literal without the
// catalog field, so every catalog-backed method dereferenced nil. The
// binaries panicked on first use; no test called any of them.

func TestNewSessionWiresCatalog(t *testing.T) {
	s := newTestSession(t, nil)
	if s.catalog == nil {
		t.Fatal("NewSession left the catalog nil")
	}
}

func TestModelsReturnsSeededCatalog(t *testing.T) {
	s := newTestSession(t, nil)
	for provider := range margo.DefaultCatalog {
		if len(s.Models(provider)) == 0 {
			t.Errorf("Models(%q) is empty; the embedded seed should always serve", provider)
		}
	}
	if got := s.Models("not-a-provider"); len(got) != 0 {
		t.Errorf("Models(unknown) = %v, want empty", got)
	}
}

func TestCatalogReturnsEveryProvider(t *testing.T) {
	s := newTestSession(t, nil)
	cat := s.Catalog()
	for provider := range margo.DefaultCatalog {
		if len(cat[provider]) == 0 {
			t.Errorf("Catalog() missing provider %q", provider)
		}
	}
}

func TestRefreshModelsErrorsForUnconfiguredProvider(t *testing.T) {
	s := newTestSession(t, nil)
	if err := s.RefreshModels(context.Background(), "openai"); err == nil {
		t.Fatal("expected an error for a provider with no key")
	}
}

// A configured provider whose client cannot list models must say so
// rather than panicking on the type assertion.
func TestRefreshModelsErrorsWhenClientCannotList(t *testing.T) {
	s := newTestSession(t, map[string]*scriptedClient{"openai": {name: "openai"}})
	err := s.RefreshModels(context.Background(), "openai")
	if err == nil || !strings.Contains(err.Error(), "does not support listing models") {
		t.Fatalf("got %v, want a does-not-support error", err)
	}
}

// RefreshStaleModels must skip clients that cannot list rather than
// failing the whole warm-up.
func TestRefreshStaleModelsSkipsNonListers(t *testing.T) {
	s := newTestSession(t, map[string]*scriptedClient{"openai": {name: "openai"}})
	if errs := s.RefreshStaleModels(context.Background()); len(errs) != 0 {
		t.Fatalf("RefreshStaleModels reported %v, want none", errs)
	}
}

// A Cancel landing between registering the run and reading back its
// context used to hand the provider client a nil context, which
// panicked on the first ctx.Done(). Pressing send then cancel
// immediately was enough. This drives that window hard.
//
// No blockUntil here on purpose: a Cancel that lands before the run is
// registered is a no-op, so a blocking producer would never be released
// and the test would deadlock rather than fail. The scripted chunks
// still evaluate ctx.Done() on every send, which is where the nil
// context blew up.
func TestStreamSurvivesImmediateCancel(t *testing.T) {
	s := newTestSession(t, map[string]*scriptedClient{
		"openai": {name: "openai", chunks: []margo.Chunk{
			{Kind: margo.ChunkText, Text: "a"},
			{Kind: margo.ChunkText, Text: "b"},
			{Usage: &margo.Usage{InputTokens: 1, OutputTokens: 1}},
		}},
	})

	var wg sync.WaitGroup
	for i := 0; i < 300; i++ {
		id := fmt.Sprintf("race-%d", i)
		wg.Add(2)
		go func() {
			defer wg.Done()
			ch, err := s.Stream(context.Background(), id, ChatRequest{Provider: "openai"})
			if err != nil {
				return
			}
			for range ch {
			}
		}()
		go func() {
			defer wg.Done()
			s.Cancel(id)
		}()
	}
	wg.Wait()
}
