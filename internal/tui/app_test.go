package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func newTestApp(onPrompt func(context.Context, string, func(Event)) error) (*app, *int) {
	var ran int
	m := newApp(context.Background(), Options{NewCommand: testTree(&ran), OnPrompt: onPrompt})
	m.input.Focus()
	return m, &ran
}

func typeText(m *app, s string) {
	for _, r := range s {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func press(m *app, code rune) tea.Cmd {
	_, cmd := m.Update(tea.KeyPressMsg{Code: code})
	return cmd
}

// isQuit reports whether cmd (possibly nested in a sequence) produces tea.QuitMsg.
func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case tea.QuitMsg:
		return true
	case tea.BatchMsg:
		for _, c := range msg {
			if isQuit(c) {
				return true
			}
		}
	}
	return false
}

func TestCtrlCOnEmptyInputQuits(t *testing.T) {
	m, _ := newTestApp(nil)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !isQuit(cmd) {
		t.Error("ctrl+c on empty input should quit")
	}
}

func TestCtrlCClearsInput(t *testing.T) {
	m, _ := newTestApp(nil)
	typeText(m, "draft")
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if isQuit(cmd) || m.input.Value() != "" {
		t.Errorf("ctrl+c should clear a draft without quitting (value %q)", m.input.Value())
	}
}

func TestQuitCommand(t *testing.T) {
	m, _ := newTestApp(nil)
	typeText(m, "/quit")
	cmd := press(m, tea.KeyEnter)
	if cmd == nil {
		t.Fatal("no command from /quit")
	}
	// The echo and the quit run in a sequence; the quit is the one that matters.
	if !m.quitting {
		t.Error("/quit should mark the model as quitting")
	}
}

func TestPromptCallsOnPrompt(t *testing.T) {
	got := make(chan string, 1)
	m, _ := newTestApp(func(_ context.Context, text string, _ func(Event)) error {
		got <- text
		return nil
	})
	typeText(m, "hello")
	if press(m, tea.KeyEnter) == nil {
		t.Fatal("submit returned no command")
	}
	if !m.busy {
		t.Error("model should be busy while the prompt runs")
	}
	if m.input.Value() != "" {
		t.Error("input should be cleared after submit")
	}

	// Run the work the way the runtime would, without the print step.
	work := func() tea.Msg { err := m.opts.OnPrompt(m.ctx, "hello", nil); return doneMsg{err: err} }
	m.Update(work())
	if text := <-got; text != "hello" {
		t.Errorf("OnPrompt got %q", text)
	}
	if m.busy {
		t.Error("model should be idle after doneMsg")
	}
}

func TestSlashCommandDoesNotReachOnPrompt(t *testing.T) {
	called := false
	m, ran := newTestApp(func(context.Context, string, func(Event)) error { called = true; return nil })
	typeText(m, "/echo hi")
	press(m, tea.KeyEnter)
	if called || *ran != 0 {
		t.Errorf("slash command leaked: OnPrompt=%v rootRan=%d", called, *ran)
	}
}

func TestTabAcceptsSuggestion(t *testing.T) {
	m, _ := newTestApp(nil)
	typeText(m, "/gr")
	if len(m.suggestions) == 0 {
		t.Fatal("expected suggestions for /gr")
	}
	press(m, tea.KeyTab)
	if got := m.input.Value(); got != "/grp " {
		t.Errorf("value after tab = %q, want %q", got, "/grp ")
	}
}

// send submits text and lets the background work finish, so the next input is
// accepted.
func send(m *app, text string) {
	typeText(m, text)
	press(m, tea.KeyEnter)
	m.Update(doneMsg{})
}

func TestHistoryUpDownRecall(t *testing.T) {
	m, _ := newTestApp(func(context.Context, string, func(Event)) error { return nil })
	send(m, "one")
	send(m, "two")

	steps := []struct {
		key  rune
		want string
	}{
		{tea.KeyUp, "two"},
		{tea.KeyUp, "one"},
		{tea.KeyUp, "one"}, // oldest stays put
		{tea.KeyDown, "two"},
		{tea.KeyDown, ""}, // the draft comes back
	}
	for i, s := range steps {
		press(m, s.key)
		if got := m.input.Value(); got != s.want {
			t.Fatalf("step %d: value = %q, want %q", i, got, s.want)
		}
	}
}

func TestHistoryPrefixFilterInModel(t *testing.T) {
	m, _ := newTestApp(func(context.Context, string, func(Event)) error { return nil })
	send(m, "foo a")
	send(m, "bar")
	send(m, "foo b")

	typeText(m, "foo")
	press(m, tea.KeyUp)
	if got := m.input.Value(); got != "foo b" {
		t.Fatalf("first up = %q, want foo b", got)
	}
	press(m, tea.KeyUp)
	if got := m.input.Value(); got != "foo a" {
		t.Fatalf("second up = %q, want foo a", got)
	}
	press(m, tea.KeyDown)
	press(m, tea.KeyDown)
	if got := m.input.Value(); got != "foo" {
		t.Errorf("down past newest = %q, want the draft foo", got)
	}
}

func TestHistoryUpMovesWithinMultilineDraft(t *testing.T) {
	m, _ := newTestApp(func(context.Context, string, func(Event)) error { return nil })
	send(m, "old")

	typeText(m, "l1")
	m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl}) // newline
	typeText(m, "l2")

	press(m, tea.KeyUp)
	if m.input.Value() != "l1\nl2" || m.input.Line() != 0 {
		t.Fatalf("first up should move the cursor to row 0, got %q at line %d", m.input.Value(), m.input.Line())
	}
	// The draft is the filter, and "old" does not start with it: nothing to recall.
	press(m, tea.KeyUp)
	if got := m.input.Value(); got != "l1\nl2" {
		t.Errorf("second up changed the draft to %q", got)
	}
}

func TestHistoryRecalledSlashCommandKeepsMenuClosed(t *testing.T) {
	m, _ := newTestApp(func(context.Context, string, func(Event)) error { return nil })
	send(m, "hello")
	send(m, "/grp")
	m.Update(doneMsg{})

	press(m, tea.KeyUp)
	if got := m.input.Value(); got != "/grp" {
		t.Fatalf("up = %q, want /grp", got)
	}
	if len(m.suggestions) != 0 {
		t.Error("recalled slash input should not open the suggestion menu")
	}
	press(m, tea.KeyUp)
	if got := m.input.Value(); got != "hello" {
		t.Errorf("second up = %q, want hello", got)
	}
}

func TestSuggestionMenuOwnsArrowsWhenTyping(t *testing.T) {
	m, _ := newTestApp(func(context.Context, string, func(Event)) error { return nil })
	send(m, "earlier")
	typeText(m, "/")
	if len(m.suggestions) == 0 {
		t.Fatal("expected suggestions for /")
	}
	press(m, tea.KeyUp)
	if got := m.input.Value(); got != "/" {
		t.Errorf("up with the menu open recalled history: %q", got)
	}
}

// resize gives the model a screen, as the terminal would.
func resize(m *app, w, h int) {
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
}

func screenRows(m *app) []string {
	return strings.Split(ansi.Strip(m.View().Content), "\n")
}

func TestEventsBuildTranscript(t *testing.T) {
	m, _ := newTestApp(nil)
	resize(m, 60, 20)
	m.Update(eventMsg{Event{Kind: EventText, Text: "let me "}})
	m.Update(eventMsg{Event{Kind: EventText, Text: "look"}})
	m.Update(eventMsg{Event{Kind: EventToolStart, Tool: "bash", Args: `{"command":"ls"}`}})
	if m.status != "running bash" {
		t.Errorf("status = %q", m.status)
	}
	m.Update(eventMsg{Event{Kind: EventToolEnd, Tool: "bash", Text: "a.go"}})
	m.Update(eventMsg{Event{Kind: EventText, Text: "done"}})
	m.Update(eventMsg{Event{Kind: EventUsage, Tokens: 12345}})

	want := "let me look\n\n✓ bash $ ls\n  │ a.go\n\ndone"
	if got := ansi.Strip(m.tr.render(60, false)); got != want {
		t.Errorf("transcript = %q, want %q", got, want)
	}
	if m.tokens != 12345 {
		t.Errorf("tokens = %d", m.tokens)
	}
}

func TestScreenFillsWindowAndPinsInput(t *testing.T) {
	m, _ := newTestApp(nil)
	m.Init()
	for _, size := range [][2]int{{80, 24}, {40, 10}, {100, 40}} {
		resize(m, size[0], size[1])
		rows := screenRows(m)
		if len(rows) != size[1] {
			t.Errorf("%v: view has %d rows", size, len(rows))
		}
		for i, r := range rows {
			if w := ansi.StringWidth(r); w > size[0] {
				t.Errorf("%v: row %d is %d cells wide", size, i, w)
			}
		}
		if !strings.Contains(rows[len(rows)-2], "╰") || !strings.Contains(rows[len(rows)-3], "Message") {
			t.Errorf("%v: input box is not just above the footer:\n%s", size, strings.Join(rows, "\n"))
		}
	}
	v := m.View()
	if !v.AltScreen {
		t.Error("the view should use the alternate screen")
	}
	if v.Cursor == nil || v.Cursor.Y != m.vp.Height()+2 {
		t.Errorf("cursor = %+v, want it on the input row below the %d-row transcript", v.Cursor, m.vp.Height())
	}
}

func TestSuggestionsStayOnScreen(t *testing.T) {
	m, _ := newTestApp(nil)
	resize(m, 80, 20)
	typeText(m, "/")
	rows := screenRows(m)
	if len(rows) != 20 {
		t.Errorf("view has %d rows with the menu open", len(rows))
	}
}

func TestFollowsOutputUnlessScrolledUp(t *testing.T) {
	m, _ := newTestApp(nil)
	resize(m, 40, 12)
	for i := 0; i < 30; i++ {
		m.addEntry(entry{kind: kindInfo, text: fmt.Sprint("line ", i)})
	}
	if !m.vp.AtBottom() {
		t.Fatal("should follow new output while at the bottom")
	}

	press(m, tea.KeyPgUp)
	if m.vp.AtBottom() {
		t.Fatal("pgup should scroll up")
	}
	top := m.vp.YOffset()
	m.addEntry(entry{kind: kindInfo, text: "new"})
	if m.vp.YOffset() != top || !m.unseen {
		t.Errorf("scrolled-up view moved (offset %d -> %d) or hint missing (unseen=%v)", top, m.vp.YOffset(), m.unseen)
	}
	if !strings.Contains(strings.Join(screenRows(m), "\n"), "new output") {
		t.Error("the 'new output' hint should show")
	}

	m.Update(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl})
	if !m.vp.AtBottom() || m.unseen {
		t.Errorf("ctrl+end should return to the bottom (unseen=%v)", m.unseen)
	}
}

func TestSubmitFollowsConversation(t *testing.T) {
	m, _ := newTestApp(nil)
	resize(m, 40, 12)
	for i := 0; i < 30; i++ {
		m.addEntry(entry{kind: kindInfo, text: "x"})
	}
	press(m, tea.KeyPgUp)
	typeText(m, "hi")
	press(m, tea.KeyEnter)
	if !m.vp.AtBottom() {
		t.Error("sending a message should scroll to the bottom")
	}
}

func TestCtrlOExpandsToolOutput(t *testing.T) {
	m, _ := newTestApp(nil)
	resize(m, 60, 30)
	m.onEvent(Event{Kind: EventToolStart, Tool: "bash", Args: `{}`})
	m.onEvent(Event{Kind: EventToolEnd, Tool: "bash", Text: strings.Repeat("row\n", 10)})
	if strings.Count(m.vp.GetContent(), "row") != maxPreviewLines {
		t.Fatalf("collapsed output has the wrong size:\n%s", m.vp.GetContent())
	}
	m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if strings.Count(m.vp.GetContent(), "row") != 10 {
		t.Errorf("ctrl+o should show all output:\n%s", m.vp.GetContent())
	}
}

func TestClearEmptiesTranscript(t *testing.T) {
	m, _ := newTestApp(nil)
	resize(m, 40, 12)
	m.addEntry(entry{kind: kindInfo, text: "something"})
	typeText(m, "/clear")
	press(m, tea.KeyEnter)
	if got := strings.TrimSpace(ansi.Strip(m.vp.GetContent())); got != "/clear" && got != "▌ /clear" {
		t.Logf("after /clear: %q", got)
	}
	if strings.Contains(m.vp.GetContent(), "something") {
		t.Error("/clear should drop earlier output")
	}
}

func TestAbortShowsNotice(t *testing.T) {
	m, _ := newTestApp(func(ctx context.Context, _ string, _ func(Event)) error { <-ctx.Done(); return ctx.Err() })
	resize(m, 40, 12)
	typeText(m, "hi")
	press(m, tea.KeyEnter)
	press(m, tea.KeyEscape)
	m.Update(doneMsg{err: context.Canceled})
	if got := ansi.Strip(m.tr.render(40, false)); !strings.HasSuffix(got, "aborted") {
		t.Errorf("transcript = %q", got)
	}
}

func TestQuitKeepsSlashQuitOutOfTranscript(t *testing.T) {
	m, _ := newTestApp(nil)
	m.addEntry(entry{kind: kindUser, text: "hello"})
	typeText(m, "/quit")
	press(m, tea.KeyEnter)
	if got := ansi.Strip(m.tr.plain(40)); got != "▌ hello" {
		t.Errorf("transcript on exit = %q", got)
	}
}
