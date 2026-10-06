package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// renderMarkdown renders the markdown that models use in chat: headings, fenced
// code, lists, quotes, rules, tables, and the inline `code`, **bold**, *italic*,
// ~~strike~~ and [links](url). It works line by line and never fails: markup it
// does not know, or that is not closed yet because the reply is still
// streaming, stays as plain text.
func renderMarkdown(text string, width int) string {
	var (
		out     []string
		inFence bool
	)
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
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
		case rule.MatchString(line):
			out = append(out, dimStyle.Render(strings.Repeat("─", max(width, 1))))
		case isTableStart(lines[i:]):
			end := i
			for end < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[end]), "|") {
				end++
			}
			out = append(out, renderTable(lines[i:end], width)...)
			i = end - 1
		case quote.MatchString(line):
			body := quote.ReplaceAllString(line, "")
			for _, r := range wrapRows(inline(body), max(width-2, 1)) {
				out = append(out, dimStyle.Render("▎ ")+r)
			}
		default:
			out = append(out, renderLine(line, width)...)
		}
	}
	return strings.Join(out, "\n")
}

var (
	heading  = regexp.MustCompile(`^#{1,6}\s+\S`)
	listItem = regexp.MustCompile(`^(\s*)([-*+]|\d+[.)])\s+(.*)$`)
	rule     = regexp.MustCompile(`^\s{0,3}([-*_])(\s*[-*_]){2,}\s*$`)
	quote    = regexp.MustCompile(`^\s{0,3}>\s?`)
	tableSep = regexp.MustCompile(`^\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?$`)

	// One alternative per kind of span, tried in this order at each position:
	// 1 code, 2-3 link, 4 bold, 5 strike, 6 italic with *, 7-9 italic with _
	// (which must start a word, so snake_case is left alone).
	inlineSpan = regexp.MustCompile("`([^`]+)`" +
		`|\[([^\]]+)\]\(([^)\s]+)\)` +
		`|\*\*([^*]+)\*\*|__([^_]+)__` +
		`|~~([^~]+)~~` +
		`|\*([^*\s](?:[^*]*[^*\s])?)\*` +
		`|(^|[\s(])_([^_\s](?:[^_]*[^_\s])?)_([\s).,;:!?]|$)`)
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

// inline styles the spans of one line of text.
func inline(s string) string {
	var (
		b    strings.Builder
		last int
	)
	for _, m := range inlineSpan.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(s[last:m[0]])
		last = m[1]
		group := func(n int) string {
			if m[2*n] < 0 {
				return ""
			}
			return s[m[2*n]:m[2*n+1]]
		}
		switch {
		case m[2] >= 0:
			b.WriteString(codeStyle.Render(group(1)))
		case m[4] >= 0:
			b.WriteString(linkText(group(2), group(3)))
		case m[8] >= 0:
			b.WriteString(boldStyle.Render(group(4)))
		case m[10] >= 0:
			b.WriteString(boldStyle.Render(group(5)))
		case m[12] >= 0:
			b.WriteString(strikeStyle.Render(group(6)))
		case m[14] >= 0:
			b.WriteString(italicStyle.Render(group(7)))
		default:
			b.WriteString(group(8));b.WriteString(italicStyle.Render(group(9)));b.WriteString(group(10))
		}
	}
	b.WriteString(s[last:])
	return b.String()
}

// linkText shows a link as its text, with the address after it unless the text
// already is the address or the file it names.
func linkText(text, url string) string {
	out := linkStyle.Render(text)
	if text != url && strings.TrimPrefix(url, "./") != text {
		out += dimStyle.Render(" (" + url + ")")
	}
	return out
}

// isTableStart reports whether lines begin with a table: a header row and a
// delimiter row. The delimiter is what tells a table from a stray "|".
func isTableStart(lines []string) bool {
	if len(lines) < 2 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "|") {
		return false
	}
	sep := strings.TrimSpace(lines[1])
	return strings.Contains(sep, "-") && tableSep.MatchString(sep)
}

func splitRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	cells := strings.Split(line, "|")
	for i, c := range cells {
		cells[i] = strings.TrimSpace(c)
	}
	return cells
}

// renderTable draws a table with aligned columns, narrowing the widest ones
// when it would not fit.
func renderTable(lines []string, width int) []string {
	var rows [][]string
	for i, l := range lines {
		if i == 1 {
			continue // the delimiter row
		}
		cells := splitRow(l)
		for j, c := range cells {
			cells[j] = inline(c)
		}
		rows = append(rows, cells)
	}
	cols := 0
	for _, r := range rows {
		cols = max(cols, len(r))
	}
	w := make([]int, cols)
	for _, r := range rows {
		for j, c := range r {
			w[j] = max(w[j], ansi.StringWidth(c))
		}
	}
	total := func() int {
		n := 3 * (cols - 1)
		for _, x := range w {
			n += x
		}
		return n
	}
	for total() > width {
		widest := 0
		for j := range w {
			if w[j] > w[widest] {
				widest = j
			}
		}
		if w[widest] <= 3 {
			break
		}
		w[widest]--
	}

	draw := func(r []string, style func(string) string) string {
		cells := make([]string, cols)
		for j := range cells {
			c := ""
			if j < len(r) {
				c = ansi.Truncate(r[j], w[j], "…")
			}
			cells[j] = style(c + strings.Repeat(" ", max(w[j]-ansi.StringWidth(c), 0)))
		}
		return strings.Join(cells, dimStyle.Render(" │ "))
	}
	plain := func(s string) string { return s }
	var out []string
	for i, r := range rows {
		if i == 0 {
			out = append(out, draw(r, func(s string) string { return boldStyle.Render(s) }))
			parts := make([]string, cols)
			for j := range parts {
				parts[j] = strings.Repeat("─", w[j])
			}
			out = append(out, dimStyle.Render(strings.Join(parts, "─┼─")))
			continue
		}
		out = append(out, draw(r, plain))
	}
	return out
}

// wrapRows word-wraps s to width and returns the rows. A width of 0 or less
// means unknown and leaves s unwrapped.
func wrapRows(s string, width int) []string {
	return strings.Split(ansi.Wrap(s, width, ""), "\n")
}
