package tui

import (
	"context"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func names(ss []suggestion) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Name
	}
	return out
}

func TestSuggest(t *testing.T) {
	m := newApp(context.Background(), Options{Commands: &fakeCommands{}, Effort: &fakeEffort{}})

	tests := []struct {
		input string
		want  []string
	}{
		{"/", []string{"clear", "echo", "effort", "grp", "help", "model", "plan", "quit", "session"}},
		{"/e", []string{"echo", "effort"}},
		{"/qui", []string{"quit"}},
		{"/grp ", []string{"grp sub"}},
		{"/grp s", []string{"grp sub"}},
		{"/nope ", nil},
		{"/echo --j", []string{"echo --json"}},
		{"/echo x", nil},
		{"/quit ", nil},
		{"/effort ", []string{"effort low", "effort medium", "effort high", "effort xhigh", "effort max"}}, // in the levels' order
		{"/effort h", []string{"effort high"}},
		{"/effort high ", nil},
		{"/plan o", []string{"plan on", "plan off"}},
		{"/model --d", []string{"model --default"}},
		{"/model --default --d", nil},
		{"hello", nil},
		{"/a\nb", nil},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := names(m.suggest(tt.input)); !reflect.DeepEqual(got, tt.want) && (len(got) > 0 || len(tt.want) > 0) {
				t.Errorf("suggest(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestModelCompletionLoadsChoicesInTheBackground(t *testing.T) {
	m := appWithModels(&fakeModels{choices: []string{"ollama/a", "openai/b"}})
	got := make(chan tea.Msg, 1)
	m.send = func(msg tea.Msg) { got <- msg }

	typeText(m, "/model o")
	if s := names(m.suggestions); len(s) != 0 {
		t.Fatalf("suggestions before the models arrive = %v", s)
	}
	m.Update(<-got)
	if s, want := names(m.suggestions), []string{"model ollama/a", "model openai/b"}; !reflect.DeepEqual(s, want) {
		t.Fatalf("suggestions = %v, want %v", s, want)
	}

	typeText(m, "p")
	if s := names(m.suggestions); !reflect.DeepEqual(s, []string{"model openai/b"}) {
		t.Errorf("narrowed suggestions = %v", s)
	}
}

func TestEnterCompletesAHalfTypedCommand(t *testing.T) {
	m, fc := newTestApp(nil)
	typeText(m, "/ec")
	press(m, tea.KeyEnter)
	if got := m.input.Value(); got != "/echo " {
		t.Fatalf("value after enter = %q, want %q", got, "/echo ")
	}
	if len(fc.ran) != 0 {
		t.Fatal("enter on a half-typed command ran it")
	}

	// The word is whole now: enter runs it.
	cmd := press(m, tea.KeyEnter)
	if cmd == nil {
		t.Fatal("second enter should run /echo")
	}
}
