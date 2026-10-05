package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type fakeModels struct {
	current string
	choices []string
	err     error
	selErr  error
}

func (f *fakeModels) Current() string { return f.current }
func (f *fakeModels) Choices(context.Context) ([]string, error) {
	return f.choices, f.err
}
func (f *fakeModels) Select(_ context.Context, ref string) error {
	if f.selErr != nil {
		return f.selErr
	}
	f.current = ref
	return nil
}

func modelTestModel(f *fakeModels) *model {
	m := newModel(context.Background(), Options{Models: f})
	m.current = f.current
	m.input.Focus()
	return m
}

// openPicker runs /model and feeds the listing back, as the runtime would.
func openPicker(t *testing.T, m *model) {
	t.Helper()
	typeText(m, "/model")
	press(m, tea.KeyEnter)
	if !m.busy {
		t.Fatal("listing models should run in the background")
	}
	m.Update(choicesMsg{items: m.opts.Models.(*fakeModels).choices})
}

func TestPickerOpensOnCurrentAndSelects(t *testing.T) {
	f := &fakeModels{current: "ollama/b", choices: []string{"ollama/a", "ollama/b", "ollama/c"}}
	m := modelTestModel(f)

	openPicker(t, m)
	if m.picker == nil || m.picker.choice() != "ollama/b" {
		t.Fatalf("picker = %+v, want it open on the current model", m.picker)
	}
	if m.busy {
		t.Error("model should be idle while the picker is open")
	}

	press(m, tea.KeyDown)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.picker != nil {
		t.Error("picker should close after a choice")
	}
	if f.current != "ollama/c" || m.current != "ollama/c" {
		t.Errorf("model = %q (footer %q), want ollama/c", f.current, m.current)
	}
}

func TestPickerEscapeKeepsModel(t *testing.T) {
	f := &fakeModels{current: "ollama/a", choices: []string{"ollama/a", "ollama/b"}}
	m := modelTestModel(f)
	openPicker(t, m)

	press(m, tea.KeyDown)
	press(m, tea.KeyEscape)
	if m.picker != nil || f.current != "ollama/a" {
		t.Errorf("picker open=%v, model=%q; want closed and unchanged", m.picker != nil, f.current)
	}
	// Typing works again.
	typeText(m, "hi")
	if m.input.Value() != "hi" {
		t.Errorf("input = %q after closing the picker", m.input.Value())
	}
}

func TestPickerSwallowsKeys(t *testing.T) {
	f := &fakeModels{choices: []string{"ollama/a"}}
	m := modelTestModel(f)
	openPicker(t, m)
	typeText(m, "x")
	if m.input.Value() != "" {
		t.Errorf("input = %q, keys must go to the picker", m.input.Value())
	}
}

func TestModelWithArgumentSwitchesDirectly(t *testing.T) {
	f := &fakeModels{current: "ollama/a"}
	m := modelTestModel(f)
	typeText(m, "/model ollama/z")
	press(m, tea.KeyEnter)
	if f.current != "ollama/z" || m.current != "ollama/z" || m.picker != nil {
		t.Errorf("model=%q footer=%q picker=%v", f.current, m.current, m.picker != nil)
	}
}

func TestModelSelectErrorKeepsCurrent(t *testing.T) {
	f := &fakeModels{current: "ollama/a", selErr: errors.New("not logged in")}
	m := modelTestModel(f)
	typeText(m, "/model openai-codex/gpt-5.5")
	press(m, tea.KeyEnter)
	if m.current != "ollama/a" {
		t.Errorf("footer = %q after a failed switch", m.current)
	}
}

func TestModelListingWithOnlyErrorsOpensNoPicker(t *testing.T) {
	f := &fakeModels{err: errors.New("ollama: connection refused")}
	m := modelTestModel(f)
	typeText(m, "/model")
	press(m, tea.KeyEnter)
	m.Update(choicesMsg{err: f.err})
	if m.picker != nil || m.busy {
		t.Errorf("picker open=%v busy=%v; want neither", m.picker != nil, m.busy)
	}
}

func TestModelWithoutModelsIsAnError(t *testing.T) {
	m, _ := newTestModel(nil)
	typeText(m, "/model")
	press(m, tea.KeyEnter)
	if m.busy || m.picker != nil {
		t.Errorf("busy=%v picker=%v", m.busy, m.picker != nil)
	}
	if got := ansi.Strip(m.tr.render(80, false)); !strings.Contains(got, "no model can be chosen") {
		t.Errorf("transcript = %q", got)
	}
}

func TestPickerWrapsAndScrolls(t *testing.T) {
	var items []string
	for i := 0; i < 25; i++ {
		items = append(items, string(rune('a'+i)))
	}
	p := newPicker("t", items, "")
	p.move(-1)
	if p.choice() != "y" || p.top != 25-pickerRows {
		t.Errorf("after wrapping up: choice=%q top=%d", p.choice(), p.top)
	}
	p.move(1)
	if p.choice() != "a" || p.top != 0 {
		t.Errorf("after wrapping down: choice=%q top=%d", p.choice(), p.top)
	}
	if !strings.Contains(p.view(), "1/25") {
		t.Errorf("view lacks a position indicator:\n%s", p.view())
	}
}

func TestFooterShowsModel(t *testing.T) {
	m := modelTestModel(&fakeModels{current: "ollama/a"})
	if v := m.View().Content; !strings.Contains(v, "ollama/a") {
		t.Errorf("footer lacks the model:\n%s", v)
	}
}
