package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/shakfu/margo/pkg/margo/core"
)

// model holds the Bubble Tea state for one TUI session. The session
// pointer is the bridge to margo-core: streaming chat reads from
// session.Stream(...) <-chan core.Event and the model dispatches each
// event back into Update as an eventMsg.
type model struct {
	session  *core.Session
	provider string
	modelID  string

	history     []core.Message // committed turns (user+assistant)
	current     strings.Builder // accumulating in-flight assistant text
	input       textinput.Model
	transcript  viewport.Model
	streaming   bool
	streamID    string
	streamCh    <-chan core.Event
	cancelFn    context.CancelFunc
	lastError   string
	width       int
	height      int
}

// eventMsg wraps a core.Event so it can travel as a tea.Msg.
type eventMsg core.Event

// streamEndMsg is delivered when the event channel closes.
type streamEndMsg struct{}

// streamStartMsg carries the freshly-opened channel from Submit so the
// model can begin pumping events from inside Update (where mutating
// fields is safe).
type streamStartMsg struct {
	id string
	ch <-chan core.Event
}

// streamErrMsg carries a synchronous error from session.Stream (the
// channel itself never opened, so there are no events to drain).
type streamErrMsg struct{ err error }

func newModel(sess *core.Session, provider, modelID string) model {
	in := textinput.New()
	in.Placeholder = "Ask anything…"
	in.Focus()
	in.Prompt = "> "
	in.CharLimit = 0

	vp := viewport.New(80, 20)
	vp.SetContent("")

	return model{
		session:    sess,
		provider:   provider,
		modelID:    modelID,
		input:      in,
		transcript: vp,
	}
}

func (m model) Init() tea.Cmd { return textinput.Blink }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			if m.streaming && m.cancelFn != nil {
				m.cancelFn()
				return m, nil
			}
			return m, tea.Quit
		case "esc":
			return m, tea.Quit
		case "enter":
			if m.streaming || strings.TrimSpace(m.input.Value()) == "" {
				return m, nil
			}
			return m, m.submit()
		}

	case streamStartMsg:
		m.streaming = true
		m.streamID = msg.id
		m.streamCh = msg.ch
		m.lastError = ""
		m.refreshTranscript()
		return m, nextEvent(msg.ch)

	case streamErrMsg:
		m.lastError = msg.err.Error()
		m.streaming = false
		return m, nil

	case eventMsg:
		return m.handleEvent(core.Event(msg))

	case streamEndMsg:
		m.streaming = false
		m.streamID = ""
		m.streamCh = nil
		m.cancelFn = nil
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *model) handleEvent(ev core.Event) (tea.Model, tea.Cmd) {
	switch ev.Kind {
	case core.EventText:
		m.current.WriteString(ev.Text)
		m.refreshTranscript()
		return m, m.pumpNext()
	case core.EventThinking:
		// Discard thinking deltas for now. A future pass can surface
		// them in a dimmed side panel or behind a Ctrl+T toggle.
		return m, m.pumpNext()
	case core.EventDone:
		if asst := m.current.String(); asst != "" {
			m.history = append(m.history, core.Message{Role: "assistant", Content: asst})
		}
		m.current.Reset()
		m.streaming = false
		m.streamID = ""
		m.streamCh = nil
		m.cancelFn = nil
		m.refreshTranscript()
		return m, nil
	case core.EventError:
		m.lastError = ev.Text
		m.current.Reset()
		m.streaming = false
		m.streamID = ""
		m.streamCh = nil
		m.cancelFn = nil
		m.refreshTranscript()
		return m, nil
	}
	// Tool / permission events would land here once StreamAgent is wired.
	return m, m.pumpNext()
}

// pumpNext schedules the next read off the active stream channel.
// Returns nil if no stream is active (e.g. after a Done/Error event).
func (m *model) pumpNext() tea.Cmd {
	if m.streamCh == nil {
		return nil
	}
	return nextEvent(m.streamCh)
}

// submit closes over the current input, appends the user turn, opens a
// core.Stream, and returns a Cmd that hands the channel back via
// streamStartMsg. Doing the channel hand-off through Update keeps the
// goroutine ordering simple (no select on m.* fields outside Update).
func (m *model) submit() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	m.input.Reset()
	m.history = append(m.history, core.Message{Role: "user", Content: text})
	m.refreshTranscript()

	id := newStreamID()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFn = cancel

	req := core.ChatRequest{
		Provider: m.provider,
		Messages: append([]core.Message(nil), m.history...),
		Options:  core.Options{Model: m.modelID, MaxTokens: 4096},
	}
	sess := m.session
	return func() tea.Msg {
		ch, err := sess.Stream(ctx, id, req)
		if err != nil {
			cancel()
			return streamErrMsg{err: err}
		}
		return streamStartMsg{id: id, ch: ch}
	}
}

// nextEvent returns a Cmd that reads exactly one event from the channel
// and re-injects it as a tea.Msg. The model schedules a fresh nextEvent
// on each event so the pump runs until the channel closes.
func nextEvent(ch <-chan core.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return streamEndMsg{}
		}
		return eventMsg(ev)
	}
}

func (m *model) layout() {
	// Leave 4 lines of chrome: header (1), separator (1), input (1),
	// hint (1). Clamp small terminals.
	body := m.height - 4
	if body < 3 {
		body = 3
	}
	m.transcript.Width = m.width
	m.transcript.Height = body
	m.input.Width = m.width - 2
	m.refreshTranscript()
}

func (m *model) refreshTranscript() {
	var b strings.Builder
	for _, turn := range m.history {
		b.WriteString(renderTurn(turn.Role, turn.Content))
		b.WriteString("\n")
	}
	if m.current.Len() > 0 {
		b.WriteString(renderTurn("assistant", m.current.String()))
		b.WriteString("\n")
	}
	if m.lastError != "" {
		b.WriteString(errorStyle.Render("error: " + m.lastError))
		b.WriteString("\n")
	}
	m.transcript.SetContent(b.String())
	m.transcript.GotoBottom()
}

func renderTurn(role, content string) string {
	label := role
	style := assistantStyle
	if role == "user" {
		style = userStyle
	}
	return style.Render(label+":") + " " + content
}

var (
	headerStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))
	userStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	assistantStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	hintStyle      = lipgloss.NewStyle().Faint(true)
	errorStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

func (m model) View() string {
	header := headerStyle.Render(fmt.Sprintf("margo — %s · %s", m.provider, m.modelID))
	status := ""
	if m.streaming {
		status = hintStyle.Render(" (streaming — Ctrl+C cancels)")
	}
	hint := hintStyle.Render("Enter: send · Ctrl+C: cancel/quit · Esc: quit")
	return strings.Join([]string{
		header + status,
		strings.Repeat("─", max(1, m.width)),
		m.transcript.View(),
		m.input.View(),
		hint,
	}, "\n")
}

func newStreamID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "tui-" + hex.EncodeToString(b)
}
