package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// renderMarkdown renders the small subset of markdown that models use in chat:
// headings, fenced code, lists, `code` and **bold**. It works line by line and
// never fails: markup it does not know, or that is not closed yet because the
// reply is still streaming, stays as plain text.
func renderMarkdown(text string, width int) string {
	var (
		out     []string
		inFence bool
	)
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			inFence = !inFence
			if inFence && len(trimmed) > 3 {
				out = append(out, dimStyle.Render("  "+trimmed[3:]))
			}
		case inFence:
			out = append(out, dimStyle.Render("│ ")+codeStyle.Render(ansi.Truncate(line, max(width-2, 1), "…")))
		case heading.MatchString(line):
			rest := strings.TrimSpace(strings.TrimLeft(line, "#"))
			out = append(out, wrapRows(headingStyle.Render(rest), width)...)
		default:
			out = append(out, renderLine(line, width)...)
		}
	}
	return strings.Join(out, "\n")
}

var (
	heading    = regexp.MustCompile(`^#{1,6}\s+\S`)
	listItem   = regexp.MustCompile(`^(\s*)([-*+]|\d+[.)])\s+(.*)$`)
	inlineSpan = regexp.MustCompile("`([^`]+)`|\\*\\*([^*]+)\\*\\*")
)

// renderLine renders a paragraph or list line, wrapped, with a hanging indent
// for lists.
func renderLine(line string, width int) []string {
	m := listItem.FindStringSubmatch(line)
	if m == nil {
		return wrapRows(inline(line), width)
	}
	bullet := m[2]
	if bullet == "-" || bullet == "*" || bullet == "+" {
		bullet = "•"
	}
	prefix := m[1] + bullet + " "
	pad := strings.Repeat(" ", ansi.StringWidth(prefix))
	rows := wrapRows(inline(m[3]), max(width-len(pad), 1))
	for i := range rows {
		if i == 0 {
			rows[i] = m[1] + dimStyle.Render(bullet) + " " + rows[i]
		} else {
			rows[i] = pad + rows[i]
		}
	}
	return rows
}

func inline(s string) string {
	return inlineSpan.ReplaceAllStringFunc(s, func(span string) string {
		if strings.HasPrefix(span, "`") {
			return codeStyle.Render(strings.Trim(span, "`"))
		}
		return boldStyle.Render(strings.Trim(span, "*"))
	})
}

// wrapRows word-wraps s to width and returns the rows. A width of 0 or less
// means unknown and leaves s unwrapped.
func wrapRows(s string, width int) []string {
	return strings.Split(ansi.Wrap(s, width, ""), "\n")
}
