package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"
)

// Clipboard is where selected text is copied to. It is a seam because the real
// one is the desktop's (xclip, wl-copy, pbcopy...).
type Clipboard interface {
	Write(text string) error
}

// systemClipboard writes to the desktop clipboard.
type systemClipboard struct{}

func (systemClipboard) Write(text string) error { return clipboard.WriteAll(text) }

// The session captures the mouse (for the wheel), which stops the terminal from
// selecting text itself. So the transcript does its own selection: drag to
// select, release to copy.

// point is a cell of the transcript: a content line and a screen column.
type point struct{ line, col int }

func (p point) before(q point) bool {
	return p.line < q.line || (p.line == q.line && p.col < q.col)
}

type selection struct {
	dragging     bool
	anchor, head point
}

// empty reports whether the selection covers nothing: no drag happened, or it
// ended where it started.
func (s selection) empty() bool { return s.anchor == s.head }

// bounds returns the selection's two ends in reading order.
func (s selection) bounds() (from, to point) {
	if s.head.before(s.anchor) {
		return s.head, s.anchor
	}
	return s.anchor, s.head
}

// span returns the cells of content line i that are selected, as [start, end),
// and whether any are. width is the line's width. The cell under the end of the
// drag is included, as terminals do.
func (s selection) span(i, width int) (start, end int, ok bool) {
	if s.empty() {
		return 0, 0, false
	}
	from, to := s.bounds()
	if i < from.line || i > to.line {
		return 0, 0, false
	}
	start, end = 0, width
	if i == from.line {
		start = from.col
	}
	if i == to.line {
		end = min(end, to.col+1)
	}
	return start, end, start < end
}

// text returns the selected text of content (the transcript as drawn, lines
// joined by newlines) without styling or trailing spaces.
func (s selection) text(content string) string {
	from, to := s.bounds()
	var out []string
	for i, line := range strings.Split(content, "\n") {
		if i < from.line || i > to.line {
			continue
		}
		plain := ansi.Strip(line)
		start, end, _ := s.span(i, ansi.StringWidth(plain)) // an empty row stays as a blank line
		out = append(out, strings.TrimRight(ansi.Cut(plain, start, end), " "))
	}
	return strings.Join(out, "\n")
}

var selectedStyle = lipgloss.NewStyle().Reverse(true)

// highlight draws the selected cells of the visible rows in reverse video.
// first is the content line shown on the first row.
func (s selection) highlight(rows []string, first int) []string {
	if s.empty() {
		return rows
	}
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row
		start, end, ok := s.span(first+i, ansi.StringWidth(row))
		if ok {
			out[i] = lipgloss.StyleRanges(row, lipgloss.NewRange(start, end, selectedStyle))
		}
	}
	return out
}

// onMouse handles a mouse press, drag or release over the transcript.
func (m *app) onMouse(msg tea.MouseMsg) tea.Cmd {
	mouse := msg.Mouse()
	switch msg.(type) {
	case tea.MouseClickMsg:
		m.sel = selection{}
		if mouse.Button != tea.MouseLeft || mouse.Y >= m.vp.Height() {
			return nil
		}
		p := m.pointAt(mouse)
		m.sel = selection{dragging: true, anchor: p, head: p}

	case tea.MouseMotionMsg:
		if !m.sel.dragging {
			return nil
		}
		// Dragging past an edge scrolls, so a selection can span more than a screen.
		switch {
		case mouse.Y < 0:
			m.vp.ScrollUp(1)
		case mouse.Y >= m.vp.Height():
			m.vp.ScrollDown(1)
		}
		m.sel.head = m.pointAt(mouse)
		m.unseen = m.unseen && !m.vp.AtBottom()

	case tea.MouseReleaseMsg:
		if !m.sel.dragging {
			return nil
		}
		m.sel.dragging = false
		if m.sel.empty() {
			m.sel = selection{}
			return nil
		}
		return m.copySelection()
	}
	return nil
}

// pointAt maps a mouse position to a cell of the transcript, clamped to it.
func (m *app) pointAt(mouse tea.Mouse) point {
	y := min(max(mouse.Y, 0), m.vp.Height()-1)
	line := min(m.vp.YOffset()+y, max(m.vp.TotalLineCount()-1, 0))
	return point{line: line, col: min(max(mouse.X, 0), max(m.width-1, 0))}
}

// copySelection puts the selected text on the clipboard. It asks the terminal
// (OSC 52, which also works over ssh) and the desktop, since terminals differ in
// which of the two they honour.
func (m *app) copySelection() tea.Cmd {
	text := m.sel.text(m.vp.GetContent())
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if m.clip != nil {
		_ = m.clip.Write(text) // the terminal may still take it
	}
	return tea.SetClipboard(text)
}
