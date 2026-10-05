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
