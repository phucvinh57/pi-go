package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

type entryKind int

const (
	kindWelcome   entryKind = iota
	kindUser                // what the user typed
	kindAssistant           // the model's reply, markdown
	kindThinking            // the model's reasoning, collapsed by default
	kindTool                // a tool call and its output
	kindInfo                // command output
	kindNotice              // a dim aside: "aborted", "no models available"
	kindError
)

// entry is one item of the conversation. Entries are stored as data and drawn
// at the current width every time the transcript renders, so a resize rewraps
// everything.
type entry struct {
	kind entryKind
	text string // user/assistant/thinking/info text; the version for kindWelcome

	tool, args, output string
	isError, done      bool

	cache, cacheKey string // the last rendering and what it was made for
}

// touch must be called after changing an entry, to drop its cached rendering.
func (e *entry) touch() { e.cacheKey = "" }

type transcript struct {
	entries []entry
}

func (t *transcript) add(e entry) { t.entries = append(t.entries, e) }

func (t *transcript) clear() { t.entries = nil }

// last returns the newest entry, or nil.
func (t *transcript) last() *entry {
	if len(t.entries) == 0 {
		return nil
	}
	return &t.entries[len(t.entries)-1]
}

// openTool returns the newest call to tool that has not finished.
func (t *transcript) openTool(tool string) *entry {
	for i := len(t.entries) - 1; i >= 0; i-- {
		if e := &t.entries[i]; e.kind == kindTool && !e.done && e.tool == tool {
			return e
		}
	}
	return nil
}

// render draws the whole conversation. expanded shows tool output and
// reasoning in full.
func (t *transcript) render(width int, expanded bool) string {
	var (
		rows []string
		prev *entry
	)
	for i := range t.entries {
		e := &t.entries[i]
		body := e.view(width, expanded)
		if body == "" {
			continue
		}
		if prev != nil && !tight(prev, e) {
			rows = append(rows, "")
		}
		rows = append(rows, body)
		prev = e
	}
	return strings.Join(rows, "\n")
}

// plain is the transcript as printed when the session ends: no welcome
// banner and no reasoning, tool output collapsed.
func (t *transcript) plain(width int) string {
	var rest transcript
	for _, e := range t.entries {
		if e.kind != kindWelcome && e.kind != kindThinking {
			rest.add(entry{kind: e.kind, text: e.text, tool: e.tool, args: e.args, output: e.output, isError: e.isError, done: e.done})
		}
	}
	return rest.render(width, false)
}

// tight reports whether two neighbours are drawn without a blank line between.
func tight(prev, cur *entry) bool {
	if prev.kind != cur.kind {
		return false
	}
	return cur.kind == kindTool || cur.kind == kindInfo || cur.kind == kindError
}

func (e *entry) view(width int, expanded bool) string {
	if width <= 0 {
		width = 80
	}
	key := fmt.Sprintf("%d/%t", width, expanded)
	if e.cacheKey != key {
		e.cache, e.cacheKey = e.draw(width, expanded), key
	}
	return e.cache
}

func (e *entry) draw(width int, expanded bool) string {
	switch e.kind {
	case kindWelcome:
		title := promptStyle.Render("pi-go")
		if e.text != "" {
			title += " " + dimStyle.Render(e.text)
		}
		hints := "/ for commands · ctrl+j newline · ctrl+o expand output · pgup/pgdn scroll"
		return title + "\n" + dimStyle.Render(ansi.Truncate(hints, width, "…"))

	case kindUser:
		rows := wrapRows(clean(e.text), width-2)
		for i, r := range rows {
			rows[i] = userBarStyle.Render("▌ ") + userTextStyle.Render(r)
		}
		return strings.Join(rows, "\n")

	case kindAssistant:
		text := strings.Trim(clean(e.text), "\n ")
		if text == "" {
			return ""
		}
		return renderMarkdown(text, width)

	case kindThinking:
		if !expanded {
			return dimStyle.Render("∴ thinking")
		}
		rows := wrapRows(strings.Trim(clean(e.text), "\n "), width-2)
		for i, r := range rows {
			prefix := "  "
			if i == 0 {
				prefix = "∴ "
			}
			rows[i] = dimStyle.Render(prefix + r)
		}
		return strings.Join(rows, "\n")

	case kindTool:
		return e.drawTool(width, expanded)

	case kindInfo:
		return strings.Join(wrapRows(clean(e.text), width), "\n")

	case kindNotice:
		return dimStyle.Render(strings.Join(wrapRows(clean(e.text), width), "\n"))

	case kindError:
		return errorStyle.Render(strings.Join(wrapRows(clean(e.text), width), "\n"))
	}
	return ""
}

func (e *entry) drawTool(width int, expanded bool) string {
	marker := toolStyle.Render("●")
	switch {
	case e.done && e.isError:
		marker = errorStyle.Render("✗")
	case e.done:
		marker = okStyle.Render("✓")
	}
	head := marker + " " + toolStyle.Render(e.tool) + " " + dimStyle.Render(summarizeArgs(e.args))
	out := []string{ansi.Truncate(head, width, "…")}

	text := strings.TrimRight(clean(e.output), "\n ")
	if text == "" && e.isError {
		text = "failed"
	}
	if !e.done || text == "" {
		return out[0]
	}
	lines := strings.Split(text, "\n")
	extra := 0
	if !expanded && len(lines) > maxPreviewLines {
		extra = len(lines) - maxPreviewLines
		lines = lines[:maxPreviewLines]
	}
	style := dimStyle
	if e.isError {
		style = errorStyle
	}
	for _, l := range lines {
		out = append(out, ansi.Truncate(style.Render("  │ "+l), width, "…"))
	}
	if extra > 0 {
		out = append(out, dimStyle.Render(fmt.Sprintf("  … %d more lines (ctrl+o to expand)", extra)))
	}
	return strings.Join(out, "\n")
}
