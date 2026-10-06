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
	saved   string // the default model, as last saved
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

func (f *fakeModels) SetDefault(ctx context.Context, ref string) error {
	if err := f.Select(ctx, ref); err != nil {
		return err
	}
	f.saved = ref
	return nil
}

func appWithModels(f *fakeModels) *app {
	m := newApp(context.Background(), Options{Models: f})
	m.current = f.current
	m.input.Focus()
	return m
}

// openPicker runs /model and feeds the listing back, as the runtime would.
func openPicker(t *testing.T, m *app) {
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
	m := appWithModels(f)

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
	m := appWithModels(f)
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
	m := appWithModels(f)
	openPicker(t, m)
	typeText(m, "x")
	if m.input.Value() != "" {
		t.Errorf("input = %q, keys must go to the picker", m.input.Value())
	}
}

func TestModelWithArgumentSwitchesDirectly(t *testing.T) {
	f := &fakeModels{current: "ollama/a"}
	m := appWithModels(f)
	typeText(m, "/model ollama/z")
	press(m, tea.KeyEnter)
	if f.current != "ollama/z" || m.current != "ollama/z" || m.picker != nil {
		t.Errorf("model=%q footer=%q picker=%v", f.current, m.current, m.picker != nil)
	}
}

func TestModelSelectErrorKeepsCurrent(t *testing.T) {
	f := &fakeModels{current: "ollama/a", selErr: errors.New("not logged in")}
	m := appWithModels(f)
	typeText(m, "/model openai-codex/gpt-5.5")
	press(m, tea.KeyEnter)
	if m.current != "ollama/a" {
		t.Errorf("footer = %q after a failed switch", m.current)
	}
}

func TestModelListingWithOnlyErrorsOpensNoPicker(t *testing.T) {
	f := &fakeModels{err: errors.New("ollama: connection refused")}
	m := appWithModels(f)
	typeText(m, "/model")
	press(m, tea.KeyEnter)
	m.Update(choicesMsg{err: f.err})
	if m.picker != nil || m.busy {
		t.Errorf("picker open=%v busy=%v; want neither", m.picker != nil, m.busy)
	}
}

func TestModelWithoutModelsIsAnError(t *testing.T) {
	m, _ := newTestApp(nil)
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
	m := appWithModels(&fakeModels{current: "ollama/a"})
	if v := m.View().Content; !strings.Contains(v, "ollama/a") {
		t.Errorf("footer lacks the model:\n%s", v)
	}
}

func TestModelDefaultFlagSavesAndSwitches(t *testing.T) {
	f := &fakeModels{current: "ollama/a"}
	m := appWithModels(f)
	typeText(m, "/model --default ollama/z")
	press(m, tea.KeyEnter)
	if f.saved != "ollama/z" || f.current != "ollama/z" || m.current != "ollama/z" {
		t.Errorf("saved=%q model=%q footer=%q", f.saved, f.current, m.current)
	}
	if last := m.tr.last(); last == nil || !strings.Contains(last.text, "saved as default") {
		t.Errorf("last entry = %+v, want a notice that it was saved", last)
	}
}

func TestModelDefaultFlagOrderDoesNotMatter(t *testing.T) {
	f := &fakeModels{current: "ollama/a"}
	m := appWithModels(f)
	typeText(m, "/model ollama/z --default")
	press(m, tea.KeyEnter)
	if f.saved != "ollama/z" {
		t.Errorf("saved = %q", f.saved)
	}
}

func TestModelWithoutDefaultFlagDoesNotSave(t *testing.T) {
	f := &fakeModels{current: "ollama/a"}
	m := appWithModels(f)
	typeText(m, "/model ollama/z")
	press(m, tea.KeyEnter)
	if f.saved != "" {
		t.Errorf("saved = %q, plain /model must not touch settings", f.saved)
	}
}

func TestModelDefaultFailureSavesNothing(t *testing.T) {
	f := &fakeModels{current: "ollama/a", selErr: errors.New("not logged in")}
	m := appWithModels(f)
	typeText(m, "/model --default openai-codex/gpt-5.5")
	press(m, tea.KeyEnter)
	if f.saved != "" || m.current != "ollama/a" {
		t.Errorf("saved=%q footer=%q after a failed switch", f.saved, m.current)
	}
}

func TestPickerDefaultSavesChoice(t *testing.T) {
	f := &fakeModels{current: "ollama/a", choices: []string{"ollama/a", "ollama/b"}}
	m := appWithModels(f)
	typeText(m, "/model --default")
	press(m, tea.KeyEnter)
	m.Update(choicesMsg{items: f.choices, asDefault: true})
	press(m, tea.KeyDown)
	press(m, tea.KeyEnter)
	if f.saved != "ollama/b" || f.current != "ollama/b" {
		t.Errorf("saved=%q model=%q", f.saved, f.current)
	}

	// A later plain picker must not inherit the default mode.
	openPicker(t, m)
	press(m, tea.KeyUp)
	press(m, tea.KeyEnter)
	if f.saved != "ollama/b" || f.current != "ollama/a" {
		t.Errorf("saved=%q model=%q; plain picker must not save", f.saved, f.current)
	}
}

type fakeEffort struct {
	level string
	err   error
}

func (f *fakeEffort) Effort() string   { return f.level }
func (f *fakeEffort) Levels() []string { return []string{"low", "medium", "high"} }
func (f *fakeEffort) SetEffort(level string) error {
	if f.err != nil {
		return f.err
	}
	f.level = level
	return nil
}

func appWithEffort(f *fakeEffort) *app {
	m := newApp(context.Background(), Options{Effort: f})
	m.effort = f.level
	m.input.Focus()
	return m
}

func TestEffortPickerSetsLevel(t *testing.T) {
	f := &fakeEffort{}
	m := appWithEffort(f)
	typeText(m, "/effort")
	press(m, tea.KeyEnter)
	if m.picker == nil || m.picker.choice() != "default" {
		t.Fatalf("picker = %+v, want it open on default", m.picker)
	}
	press(m, tea.KeyDown)
	press(m, tea.KeyDown) // default, low, medium
	press(m, tea.KeyEnter)
	if m.picker != nil || f.level != "medium" || m.effort != "medium" {
		t.Errorf("picker open=%v, effort=%q, footer=%q; want closed and medium", m.picker != nil, f.level, m.effort)
	}
}

func TestEffortWithArgumentAndDefault(t *testing.T) {
	f := &fakeEffort{}
	m := appWithEffort(f)
	typeText(m, "/effort high")
	press(m, tea.KeyEnter)
	if f.level != "high" || m.effort != "high" {
		t.Fatalf("effort = %q (footer %q), want high", f.level, m.effort)
	}
	typeText(m, "/effort default")
	press(m, tea.KeyEnter)
	if f.level != "" || m.effort != "" {
		t.Errorf("effort = %q (footer %q), want the model's default", f.level, m.effort)
	}
}

func TestEffortErrorKeepsLevel(t *testing.T) {
	f := &fakeEffort{level: "low", err: errors.New("unknown effort")}
	m := appWithEffort(f)
	typeText(m, "/effort turbo")
	press(m, tea.KeyEnter)
	if m.effort != "low" || m.tr.last().kind != kindError {
		t.Errorf("footer = %q, last = %+v; want low and an error", m.effort, m.tr.last())
	}
}

func TestEffortWithoutEffortIsAnError(t *testing.T) {
	m := newApp(context.Background(), Options{})
	m.input.Focus()
	typeText(m, "/effort low")
	press(m, tea.KeyEnter)
	if m.tr.last().kind != kindError {
		t.Errorf("last = %+v, want an error", m.tr.last())
	}
}
