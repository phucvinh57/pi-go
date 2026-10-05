package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func newTestModel(onPrompt func(context.Context, string) (string, error)) (*model, *int) {
	var ran int
	m := newModel(context.Background(), Options{NewCommand: testTree(&ran), OnPrompt: onPrompt})
	m.input.Focus()
	return m, &ran
}

func typeText(m *model, s string) {
	for _, r := range s {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func press(m *model, code rune) tea.Cmd {
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
	m, _ := newTestModel(nil)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !isQuit(cmd) {
		t.Error("ctrl+c on empty input should quit")
	}
}

func TestCtrlCClearsInput(t *testing.T) {
	m, _ := newTestModel(nil)
	typeText(m, "draft")
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if isQuit(cmd) || m.input.Value() != "" {
		t.Errorf("ctrl+c should clear a draft without quitting (value %q)", m.input.Value())
	}
}

func TestQuitCommand(t *testing.T) {
	m, _ := newTestModel(nil)
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
	m, _ := newTestModel(func(_ context.Context, text string) (string, error) {
		got <- text
		return "reply", nil
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
	work := func() tea.Msg { text, err := m.opts.OnPrompt(m.ctx, "hello"); return doneMsg{text, err} }
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
	m, ran := newTestModel(func(context.Context, string) (string, error) { called = true; return "", nil })
	typeText(m, "/echo hi")
	press(m, tea.KeyEnter)
	if called || *ran != 0 {
		t.Errorf("slash command leaked: OnPrompt=%v rootRan=%d", called, *ran)
	}
}

func TestTabAcceptsSuggestion(t *testing.T) {
	m, _ := newTestModel(nil)
	typeText(m, "/gr")
	if len(m.suggestions) == 0 {
		t.Fatal("expected suggestions for /gr")
	}
	press(m, tea.KeyTab)
	if got := m.input.Value(); got != "/grp " {
		t.Errorf("value after tab = %q, want %q", got, "/grp ")
	}
}

func TestClearReturnsCommand(t *testing.T) {
	m, _ := newTestModel(nil)
	if cmd := runClear(m, nil); cmd == nil {
		t.Error("/clear should return a screen-clearing command")
	}
}

// send submits text and lets the background work finish, so the next input is
// accepted.
func send(m *model, text string) {
	typeText(m, text)
	press(m, tea.KeyEnter)
	m.Update(doneMsg{})
}

func TestHistoryUpDownRecall(t *testing.T) {
	m, _ := newTestModel(func(context.Context, string) (string, error) { return "", nil })
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
	m, _ := newTestModel(func(context.Context, string) (string, error) { return "", nil })
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
	m, _ := newTestModel(func(context.Context, string) (string, error) { return "", nil })
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
	m, _ := newTestModel(func(context.Context, string) (string, error) { return "", nil })
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
	m, _ := newTestModel(func(context.Context, string) (string, error) { return "", nil })
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
