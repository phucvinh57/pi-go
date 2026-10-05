package tui

import (
	"fmt"
	"strings"
)

const pickerRows = 10

// picker is a list the user walks with the arrow keys. It replaces the input
// line while it is open.
type picker struct {
	title    string
	items    []string
	current  string // marked in the list; the list opens on it
	selected int
	top      int
}

func newPicker(title string, items []string, current string) *picker {
	p := &picker{title: title, items: items, current: current}
	for i, it := range items {
		if it == current {
			p.selected = i
			break
		}
	}
	p.scroll()
	return p
}

// move steps the selection by delta rows, wrapping at both ends.
func (p *picker) move(delta int) {
	n := len(p.items)
	p.selected = ((p.selected+delta)%n + n) % n
	p.scroll()
}

func (p *picker) scroll() {
	switch {
	case p.selected < p.top:
		p.top = p.selected
	case p.selected >= p.top+pickerRows:
		p.top = p.selected - pickerRows + 1
	}
}

func (p *picker) choice() string { return p.items[p.selected] }

func (p *picker) view() string {
	var b strings.Builder
	b.WriteString(promptStyle.Render(p.title))
	b.WriteString(dimStyle.Render("  ↑/↓ move · enter select · esc cancel"))

	end := min(p.top+pickerRows, len(p.items))
	for i := p.top; i < end; i++ {
		it := p.items[i]
		mark := ""
		if it == p.current {
			mark = dimStyle.Render("  (current)")
		}
		if i == p.selected {
			b.WriteString("\n" + selStyle.Render("› "+it) + mark)
		} else {
			b.WriteString("\n  " + it + mark)
		}
	}
	if len(p.items) > pickerRows {
		b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("  %d/%d", p.selected+1, len(p.items))))
	}
	return b.String()
}
