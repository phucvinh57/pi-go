package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

type fakeClipboard struct{ got string }

func (f *fakeClipboard) Write(s string) error { f.got = s; return nil }

func selectionApp(t *testing.T) (*app, *fakeClipboard) {
	t.Helper()
	clip := &fakeClipboard{}
	m := newApp(context.Background(), Options{Clipboard: clip})
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	m.tr.add(entry{kind: kindInfo, text: "hello world\nsecond line\nthird"})
	m.refresh(false)
	return m, clip
}

// screenRow finds the screen row that shows text.
func screenRow(t *testing.T, m *app, text string) int {
	t.Helper()
	for i, l := range strings.Split(m.vp.View(), "\n") {
		if strings.Contains(l, text) {
			return i
		}
	}
	t.Fatalf("%q is not on screen", text)
	return 0
}

func drag(m *app, x0, y0, x1, y1 int) tea.Cmd {
	m.Update(tea.MouseClickMsg{X: x0, Y: y0, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: x1, Y: y1, Button: tea.MouseLeft})
	_, cmd := m.Update(tea.MouseReleaseMsg{X: x1, Y: y1, Button: tea.MouseLeft})
	return cmd
}

func TestDragCopiesSelectedText(t *testing.T) {
	m, clip := selectionApp(t)
	y := screenRow(t, m, "hello world")
	cmd := drag(m, 6, y, 10, y)
	if clip.got != "world" {
		t.Errorf("clipboard = %q, want %q", clip.got, "world")
	}
	if cmd == nil {
		t.Error("the terminal clipboard should be set too")
	}
	if !strings.Contains(m.View().Content, "copied") {
		t.Error("the status row should say the text was copied")
	}
}

func TestDragAcrossLines(t *testing.T) {
	m, clip := selectionApp(t)
	y := screenRow(t, m, "hello world")
	drag(m, 6, y, 4, y+2)
	if want := "world\nsecond line\nthird"; clip.got != want {
		t.Errorf("clipboard = %q, want %q", clip.got, want)
	}
}

func TestClickWithoutDragCopiesNothing(t *testing.T) {
	m, clip := selectionApp(t)
	y := screenRow(t, m, "hello world")
	if cmd := drag(m, 3, y, 3, y); cmd != nil || clip.got != "" {
		t.Errorf("a click should not copy, got %q", clip.got)
	}
}

func TestSelectionClearsOnKey(t *testing.T) {
	m, _ := selectionApp(t)
	y := screenRow(t, m, "hello world")
	drag(m, 0, y, 4, y)
	typeText(m, "x")
	if !m.sel.empty() || m.copied {
		t.Error("typing should clear the selection")
	}
}

func TestSelectionIsHighlighted(t *testing.T) {
	m, _ := selectionApp(t)
	y := screenRow(t, m, "hello world")
	m.Update(tea.MouseClickMsg{X: 0, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: 4, Y: y, Button: tea.MouseLeft})
	if m.transcriptView() == m.vp.View() {
		t.Error("the dragged cells should be drawn differently")
	}
}
