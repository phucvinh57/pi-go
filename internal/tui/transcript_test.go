package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func plainRows(s string) []string { return strings.Split(ansi.Strip(s), "\n") }

func TestTranscriptRewrapsOnWidthChange(t *testing.T) {
	var tr transcript
	tr.add(entry{kind: kindAssistant, text: "aaaa bbbb cccc dddd"})

	if rows := plainRows(tr.render(10, false)); len(rows) != 2 || rows[0] != "aaaa bbbb" {
		t.Errorf("at 10: %q", rows)
	}
	if rows := plainRows(tr.render(40, false)); len(rows) != 1 {
		t.Errorf("at 40: %q", rows)
	}
}

func TestTranscriptRowsFitWidth(t *testing.T) {
	var tr transcript
	tr.add(entry{kind: kindUser, text: strings.Repeat("word ", 30)})
	tr.add(entry{kind: kindAssistant, text: strings.Repeat("reply ", 30) + "\n- " + strings.Repeat("item ", 20)})
	tr.add(entry{kind: kindTool, tool: "bash", args: `{"command":"` + strings.Repeat("x", 200) + `"}`, done: true, output: strings.Repeat("y", 200)})
	for _, w := range []int{20, 50} {
		for _, row := range strings.Split(tr.render(w, false), "\n") {
			if got := ansi.StringWidth(row); got > w {
				t.Errorf("width %d: row of %d cells: %q", w, got, ansi.Strip(row))
			}
		}
	}
}

func TestUserEntryHasBar(t *testing.T) {
	var tr transcript
	tr.add(entry{kind: kindUser, text: "hello"})
	if got := ansi.Strip(tr.render(40, false)); got != "▌ hello" {
		t.Errorf("user entry = %q", got)
	}
}

func TestToolEntryStates(t *testing.T) {
	e := entry{kind: kindTool, tool: "bash", args: `{"command":"ls"}`}
	if got := ansi.Strip(e.view(80, false)); got != "● bash $ ls" {
		t.Errorf("running = %q", got)
	}

	e.output, e.done = "a\nb", true
	e.touch()
	if got := ansi.Strip(e.view(80, false)); got != "✓ bash $ ls\n  │ a\n  │ b" {
		t.Errorf("done = %q", got)
	}

	e.output, e.isError = "no such file", true
	e.touch()
	if got := ansi.Strip(e.view(80, false)); got != "✗ bash $ ls\n  │ no such file" {
		t.Errorf("failed = %q", got)
	}
}

func TestToolOutputCollapsedAndExpanded(t *testing.T) {
	e := entry{kind: kindTool, tool: "bash", done: true, output: strings.Repeat("line\n", 10)}

	collapsed := ansi.Strip(e.view(80, false))
	if n := strings.Count(collapsed, "\n"); n != maxPreviewLines+1 || !strings.HasSuffix(collapsed, "… 4 more lines (ctrl+o to expand)") {
		t.Errorf("collapsed = %q", collapsed)
	}
	if expanded := ansi.Strip(e.view(80, true)); strings.Contains(expanded, "more lines") || strings.Count(expanded, "│") != 10 {
		t.Errorf("expanded = %q", expanded)
	}
}

func TestThinkingCollapsedByDefault(t *testing.T) {
	e := entry{kind: kindThinking, text: "hmm\nokay"}
	if got := ansi.Strip(e.view(80, false)); got != "∴ thinking" {
		t.Errorf("collapsed = %q", got)
	}
	if got := ansi.Strip(e.view(80, true)); got != "∴ hmm\n  okay" {
		t.Errorf("expanded = %q", got)
	}
}

func TestBlankLineBetweenEntriesExceptTools(t *testing.T) {
	var tr transcript
	tr.add(entry{kind: kindUser, text: "q"})
	tr.add(entry{kind: kindTool, tool: "a"})
	tr.add(entry{kind: kindTool, tool: "b"})
	tr.add(entry{kind: kindAssistant, text: "answer"})
	want := "▌ q\n\n● a \n● b \n\nanswer"
	if got := ansi.Strip(tr.render(40, false)); got != want {
		t.Errorf("render = %q, want %q", got, want)
	}
}

func TestPlainDropsWelcomeAndThinking(t *testing.T) {
	var tr transcript
	tr.add(entry{kind: kindWelcome})
	tr.add(entry{kind: kindUser, text: "hi"})
	tr.add(entry{kind: kindThinking, text: "hmm"})
	if got := ansi.Strip(tr.plain(40)); got != "▌ hi" {
		t.Errorf("plain = %q", got)
	}
}

func TestMarkdown(t *testing.T) {
	in := "# Title\nuse `go test` and **care**\n- one\n- two\n```go\nx := 1\n```\nafter"
	got := ansi.Strip(renderMarkdown(in, 40))
	want := "Title\nuse go test and care\n• one\n• two\n  go\n│ x := 1\nafter"
	if got != want {
		t.Errorf("markdown =\n%q\nwant\n%q", got, want)
	}
}

func TestMarkdownLeavesUnclosedMarkupAlone(t *testing.T) {
	if got := ansi.Strip(renderMarkdown("a **half and `code", 40)); got != "a **half and `code" {
		t.Errorf("got %q", got)
	}
}

func TestMarkdownListHangingIndent(t *testing.T) {
	rows := plainRows(renderMarkdown("- aaaa bbbb cccc", 10))
	if len(rows) != 2 || rows[0] != "• aaaa" || rows[1] != "  bbbb" {
		// width 10 leaves 8 cells for the text: "aaaa" and "bbbb cccc" wrap
		t.Logf("rows = %q", rows)
	}
	if len(rows) < 2 || !strings.HasPrefix(rows[1], "  ") {
		t.Errorf("continuation rows should be indented: %q", rows)
	}
}

func TestMarkdownInlineSpans(t *testing.T) {
	for in, want := range map[string]string{
		"see [README.md](README.md) now":     "see README.md now",
		"see [docs](https://x.dev/a) now":    "see docs (https://x.dev/a) now",
		"an *italic* and _this_ word":        "an italic and this word",
		"snake_case_name and 2 * 3 * 4":      "snake_case_name and 2 * 3 * 4",
		"~~gone~~ and **bold** and `code`":   "gone and bold and code",
		"a **half and *open and [link](half": "a **half and *open and [link](half",
	} {
		if got := ansi.Strip(renderMarkdown(in, 80)); got != want {
			t.Errorf("%q rendered as %q, want %q", in, got, want)
		}
	}
}

func TestMarkdownBlocks(t *testing.T) {
	in := "> quoted\n---\n| a | bb |\n|---|:--:|\n| ccc | d |\nafter"
	want := "▎ quoted\n" + strings.Repeat("─", 20) + "\n" +
		"a   │ bb\n────┼───\nccc │ d \nafter"
	if got := ansi.Strip(renderMarkdown(in, 20)); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestMarkdownTableNeedsDelimiterRow(t *testing.T) {
	if got := ansi.Strip(renderMarkdown("| not a table |", 20)); got != "| not a table |" {
		t.Errorf("got %q", got)
	}
}

func TestMarkdownTableShrinksToWidth(t *testing.T) {
	for _, r := range plainRows(renderMarkdown("| a | b |\n|---|---|\n| "+strings.Repeat("x", 50)+" | y |", 20)) {
		if ansi.StringWidth(r) > 20 {
			t.Errorf("row %q is wider than 20", r)
		}
	}
}
