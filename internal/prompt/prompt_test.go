package prompt

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func press(m *model, keys ...rune) {
	for _, k := range keys {
		m.Update(tea.KeyPressMsg{Code: k})
	}
}

func TestModelMovesAndWraps(t *testing.T) {
	m := newModel("pick", []string{"a", "b", "c"})

	press(m, 'j')
	if m.selected != 1 {
		t.Fatalf("after down: selected = %d, want 1", m.selected)
	}
	press(m, 'k', 'k')
	if m.selected != 2 {
		t.Fatalf("up from the top should wrap: selected = %d, want 2", m.selected)
	}
	press(m, 'j')
	if m.selected != 0 {
		t.Fatalf("down from the bottom should wrap: selected = %d, want 0", m.selected)
	}
}

func TestModelEnterChooses(t *testing.T) {
	m := newModel("pick", []string{"a", "b"})
	press(m, 'j')
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if !m.done || m.cancelled || m.selected != 1 {
		t.Fatalf("done=%v cancelled=%v selected=%d, want done, not cancelled, 1", m.done, m.cancelled, m.selected)
	}
	if got := m.View().Content; !strings.Contains(got, "b") || strings.Contains(got, "a") {
		t.Fatalf("final view should show only the choice, got %q", got)
	}
}

func TestModelEscCancels(t *testing.T) {
	m := newModel("pick", []string{"a", "b"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	if !m.cancelled {
		t.Fatal("esc should cancel")
	}
}

func TestFieldEditsAndReports(t *testing.T) {
	f := Field{Secret: true}
	for _, r := range "abc" {
		f.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	f.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	f.Update(tea.PasteMsg{Content: " xy\n"})
	if got := f.Value(); got != "abxy" {
		t.Fatalf("value = %q, want abxy", got)
	}
	if strings.Contains(f.View(), "ab") || !strings.Contains(f.View(), "••••") {
		t.Fatalf("a secret must be drawn as bullets: %q", f.View())
	}

	if s := f.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); s != Submitted {
		t.Fatalf("enter: state = %v, want Submitted", s)
	}
	if s := f.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); s != Dismissed {
		t.Fatalf("ctrl+c: state = %v, want Dismissed", s)
	}
}

func TestInputModelCtrlCDismisses(t *testing.T) {
	m := &inputModel{title: "Key:"}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if m.state != Dismissed || cmd == nil {
		t.Fatalf("state = %v, cmd nil = %v; ctrl+c must dismiss and quit the prompt", m.state, cmd == nil)
	}
}

func TestLinesReadsInputAndStopsOnCancel(t *testing.T) {
	l := NewLines(strings.NewReader("sk-1\n"), io.Discard)
	if got, err := l.Input(context.Background(), "Key:", true); err != nil || got != "sk-1\n" {
		t.Fatalf("Input = %q, %v", got, err)
	}
	if _, err := l.Input(context.Background(), "Key:", true); !errors.Is(err, io.EOF) {
		t.Fatalf("at the end of input: err = %v, want io.EOF", err)
	}
	if _, err := l.Select(context.Background(), "x", []string{"a"}); !errors.Is(err, ErrNoTerminal) {
		t.Fatalf("Select err = %v, want ErrNoTerminal", err)
	}

	pr, pw := io.Pipe()
	defer pw.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewLines(pr, io.Discard).Input(ctx, "Key:", true); !errors.Is(err, context.Canceled) {
		t.Fatalf("blocked input with a cancelled ctx: err = %v, want context.Canceled", err)
	}
}
