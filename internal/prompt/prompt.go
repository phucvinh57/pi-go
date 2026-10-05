// Package prompt asks the user to choose from a short list in a terminal.
package prompt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"golang.org/x/term"
)

// ErrCancelled is returned when the user dismisses the list without choosing.
var ErrCancelled = errors.New("cancelled")

// Terminal is a Prompter that draws inline (no alternate screen) on Out and
// reads keys from In. Raw mode means ctrl+c arrives as a key, not a signal, so
// every question can be dismissed.
type Terminal struct {
	In  io.Reader
	Out io.Writer
}

// Interactive reports whether in and out are both terminals, i.e. whether a
// Terminal can work at all.
func Interactive(in io.Reader, out io.Writer) bool {
	return isTerminal(in) && isTerminal(out)
}

func isTerminal(v any) bool {
	f, ok := v.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func (t Terminal) Select(ctx context.Context, title string, options []string) (int, error) {
	if len(options) == 0 {
		return 0, errors.New("nothing to choose from")
	}
	m := newModel(title, options)
	final, err := tea.NewProgram(m,
		tea.WithContext(ctx), tea.WithInput(t.In), tea.WithOutput(t.Out)).Run()
	if err != nil {
		return 0, err
	}
	if m = final.(*model); m.cancelled {
		return 0, ErrCancelled
	}
	return m.selected, nil
}

// Input asks for one line of text inline.
func (t Terminal) Input(ctx context.Context, title string, secret bool) (string, error) {
	m := &inputModel{title: title, field: Field{Secret: secret}}
	final, err := tea.NewProgram(m,
		tea.WithContext(ctx), tea.WithInput(t.In), tea.WithOutput(t.Out)).Run()
	if err != nil {
		return "", err
	}
	if m = final.(*inputModel); m.state == Dismissed {
		return "", ErrCancelled
	}
	return m.field.Value(), nil
}

type inputModel struct {
	title string
	field Field
	state State
}

func (m *inputModel) Init() tea.Cmd { return nil }

func (m *inputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.state = m.field.Update(msg); m.state != Editing {
		return m, tea.Quit
	}
	return m, nil
}

func (m *inputModel) View() tea.View {
	switch m.state {
	case Submitted:
		return tea.NewView(titleStyle.Render(m.title) + " " + m.field.Done() + "\n")
	case Dismissed:
		return tea.NewView("")
	}
	return tea.NewView(titleStyle.Render(m.title) + " " + m.field.View() +
		dimStyle.Render("  enter submit · esc cancel"))
}

var (
	titleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	selStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
)

type model struct {
	title     string
	options   []string
	selected  int
	done      bool
	cancelled bool
}

func newModel(title string, options []string) *model {
	return &model{title: title, options: options}
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	n := len(m.options)
	switch key.String() {
	case "up", "k":
		m.selected = (m.selected - 1 + n) % n
	case "down", "j":
		m.selected = (m.selected + 1) % n
	case "enter":
		m.done = true
		return m, tea.Quit
	case "esc", "ctrl+c", "q":
		m.done, m.cancelled = true, true
		return m, tea.Quit
	}
	return m, nil
}

func (m *model) View() tea.View {
	if m.done {
		// Leave the choice behind in the scrollback.
		if m.cancelled {
			return tea.NewView("")
		}
		return tea.NewView(fmt.Sprintf("%s %s\n", titleStyle.Render(m.title), m.options[m.selected]))
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render(m.title))
	b.WriteString(dimStyle.Render("  ↑/↓ move · enter select · esc cancel"))
	for i, opt := range m.options {
		if i == m.selected {
			b.WriteString("\n" + selStyle.Render("› "+opt))
		} else {
			b.WriteString("\n  " + opt)
		}
	}
	return tea.NewView(b.String())
}
