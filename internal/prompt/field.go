package prompt

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// maxShown caps how much of the value is drawn, so a long key or URL never
// wraps the line it sits on.
const maxShown = 48

// Field is a single-line text entry. It is a plain value, not a tea.Model, so
// the terminal prompt and the full-screen session can both embed it.
type Field struct {
	Secret bool // draw bullets instead of the text

	value []rune
}

// State is what a key press did to a Field.
type State int

const (
	Editing State = iota
	Submitted
	Dismissed
)

// Value returns what has been typed.
func (f *Field) Value() string { return string(f.value) }

// Update applies a key press or a paste and reports whether the user is done.
func (f *Field) Update(msg tea.Msg) State {
	switch msg := msg.(type) {
	case tea.PasteMsg:
		// A pasted key or URL often ends in a newline; it is not a submit.
		f.value = append(f.value, []rune(strings.Join(strings.Fields(msg.Content), ""))...)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "enter":
			return Submitted
		case "esc", "ctrl+c":
			return Dismissed
		case "backspace":
			if n := len(f.value); n > 0 {
				f.value = f.value[:n-1]
			}
		case "ctrl+u":
			f.value = nil
		default:
			if msg.Text != "" && msg.Mod&(tea.ModCtrl|tea.ModAlt) == 0 {
				f.value = append(f.value, []rune(msg.Text)...)
			}
		}
	}
	return Editing
}

// View draws the value followed by a cursor.
func (f *Field) View() string {
	shown := f.value
	prefix := ""
	if len(shown) > maxShown {
		shown, prefix = shown[len(shown)-maxShown:], "…"
	}
	text := string(shown)
	if f.Secret {
		text = strings.Repeat("•", len(shown))
	}
	return prefix + text + lipgloss.NewStyle().Reverse(true).Render(" ")
}

// Done is the line left behind once the field is submitted: the value, or a
// note that it was hidden.
func (f *Field) Done() string {
	if f.Secret {
		return dimStyle.Render("(hidden)")
	}
	return f.Value()
}
