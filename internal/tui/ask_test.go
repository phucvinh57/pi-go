package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/phucvinh57/pi-go/internal/prompt"
)

// askApp returns an app whose asker's messages are delivered by hand.
func askApp() (*app, asker, chan tea.Msg) {
	m, _ := newTestApp(nil)
	msgs := make(chan tea.Msg, 4)
	m.send = func(msg tea.Msg) { msgs <- msg }
	return m, asker{m.send}, msgs
}

type answer struct {
	idx  int
	text string
	err  error
}

// ask runs q on its own goroutine, as a command would, and opens the question
// in m once it has been sent.
func ask(t *testing.T, m *app, msgs chan tea.Msg, q func() answer) chan answer {
	t.Helper()
	out := make(chan answer, 1)
	go func() { out <- q() }()
	select {
	case msg := <-msgs:
		m.Update(msg)
	case <-time.After(time.Second):
		t.Fatal("no question reached the session")
	}
	if m.ask == nil {
		t.Fatal("the question did not open")
	}
	return out
}

func result(t *testing.T, ch chan answer) answer {
	t.Helper()
	select {
	case a := <-ch:
		return a
	case <-time.After(time.Second):
		t.Fatal("the asker never got an answer")
		return answer{}
	}
}

func TestAskInputTypedAndSubmitted(t *testing.T) {
	m, a, msgs := askApp()
	ch := ask(t, m, msgs, func() answer {
		s, err := a.Input(context.Background(), "Key:", true)
		return answer{text: s, err: err}
	})

	typeText(m, "sk-1")
	m.Update(tea.PasteMsg{Content: "23\n"})
	if view := m.View().Content; strings.Contains(view, "sk-123") || !strings.Contains(view, "••••••") {
		t.Fatalf("a secret must be drawn as bullets, got:\n%s", view)
	}
	press(m, tea.KeyEnter)

	if got := result(t, ch); got.err != nil || got.text != "sk-123" {
		t.Fatalf("answer = %+v, want sk-123", got)
	}
	if m.ask != nil {
		t.Fatal("the question stays open after the answer")
	}
}

func TestAskCtrlCAndEscCancel(t *testing.T) {
	for name, k := range map[string]tea.KeyPressMsg{
		"ctrl+c": {Code: 'c', Mod: tea.ModCtrl},
		"esc":    {Code: tea.KeyEscape},
	} {
		t.Run(name, func(t *testing.T) {
			m, a, msgs := askApp()
			ch := ask(t, m, msgs, func() answer {
				_, err := a.Input(context.Background(), "Key:", true)
				return answer{err: err}
			})

			_, cmd := m.Update(k)
			if isQuit(cmd) {
				t.Fatal("ctrl+c at a question must cancel it, not quit the session")
			}
			if got := result(t, ch); !errors.Is(got.err, prompt.ErrCancelled) {
				t.Fatalf("err = %v, want ErrCancelled", got.err)
			}
			if m.ask != nil {
				t.Fatal("the question stays open after cancelling")
			}
		})
	}
}

func TestAskSelectChoosesWithArrows(t *testing.T) {
	m, a, msgs := askApp()
	ch := ask(t, m, msgs, func() answer {
		i, err := a.Select(context.Background(), "Pick:", []string{"a", "b", "c"})
		return answer{idx: i, err: err}
	})

	press(m, tea.KeyDown)
	press(m, tea.KeyDown)
	press(m, tea.KeyEnter)

	if got := result(t, ch); got.err != nil || got.idx != 2 {
		t.Fatalf("answer = %+v, want index 2", got)
	}
}

func TestAskClosesWhenTheAskerGivesUp(t *testing.T) {
	m, a, msgs := askApp()
	ctx, cancel := context.WithCancel(context.Background())
	ch := ask(t, m, msgs, func() answer {
		_, err := a.Input(ctx, "Redirect URL:", false)
		return answer{err: err}
	})

	cancel()
	if got := result(t, ch); !errors.Is(got.err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", got.err)
	}
	select {
	case msg := <-msgs:
		m.Update(msg)
	case <-time.After(time.Second):
		t.Fatal("the session was never told to close the question")
	}
	if m.ask != nil {
		t.Fatal("the question stays open although its asker is gone")
	}
}
