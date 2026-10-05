package tui

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// writeTree creates files (relative path -> content) under a temp dir.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestTrailingTag(t *testing.T) {
	tests := []struct {
		input string
		query string
		ok    bool
	}{
		{"@", "", true},
		{"@ma", "ma", true},
		{"look at @internal/tui/a", "internal/tui/a", true},
		{"two\nlines @x", "x", true},
		{"mail me@example.com", "", false},
		{"@done ", "", false},
		{"plain", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			q, ok := trailingTag(tt.input)
			if q != tt.query || ok != tt.ok {
				t.Errorf("trailingTag(%q) = %q, %v; want %q, %v", tt.input, q, ok, tt.query, tt.ok)
			}
		})
	}
}

func TestMatchFiles(t *testing.T) {
	files := []string{"README.md", "cmd/pi-go/main.go", "internal/tui/app.go", "internal/tui/app_test.go", "main_test.go"}
	tests := []struct {
		query string
		want  []string
	}{
		{"", []string{"README.md", "main_test.go", "cmd/pi-go/main.go", "internal/tui/app.go", "internal/tui/app_test.go"}},
		{"app", []string{"internal/tui/app.go", "internal/tui/app_test.go"}},
		{"main", []string{"main_test.go", "cmd/pi-go/main.go"}},
		{"internal/tui", []string{"internal/tui/app.go", "internal/tui/app_test.go"}},
		{"APP.G", []string{"internal/tui/app.go", "internal/tui/app_test.go"}}, // case-insensitive; the exact name ranks first
		{"itap", []string{"internal/tui/app.go", "internal/tui/app_test.go"}},  // letters in order
		{"zzz", nil},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got := matchFiles(files, tt.query)
			if !reflect.DeepEqual(got, tt.want) && (len(got) > 0 || len(tt.want) > 0) {
				t.Errorf("matchFiles(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestListFilesWalk(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a.go":              "x",
		"sub/b.go":          "x",
		".hidden/c.go":      "x",
		"node_modules/d.js": "x",
		"with space.txt":    "x",
	})
	got := listFiles(root)
	want := []string{"a.go", "sub/b.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("listFiles = %v, want %v", got, want)
	}
}

func TestExpandTags(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a.go":      "package a\n",
		"dir/b.txt": "hello",
		"bin":       "ab\x00cd",
	})

	t.Run("attaches files once", func(t *testing.T) {
		got, paths := expandTags(root, "compare @a.go with @dir/b.txt, and @a.go again")
		if want := []string{"a.go", "dir/b.txt"}; !reflect.DeepEqual(paths, want) {
			t.Fatalf("paths = %v, want %v", paths, want)
		}
		for _, want := range []string{
			"compare @a.go with @dir/b.txt, and @a.go again\n\n",
			"<file path=\"a.go\">\npackage a\n</file>",
			"<file path=\"dir/b.txt\">\nhello\n</file>",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("expanded text lacks %q:\n%s", want, got)
			}
		}
		if strings.Count(got, "<file ") != 2 {
			t.Errorf("a.go should be attached once:\n%s", got)
		}
	})

	t.Run("leaves other text alone", func(t *testing.T) {
		for _, text := range []string{"no tags", "@missing.go", "@dir", "@bin", "me@a.go", "@"} {
			if got, paths := expandTags(root, text); got != text || paths != nil {
				t.Errorf("expandTags(%q) = %q, %v; want it unchanged", text, got, paths)
			}
		}
	})

	t.Run("trailing punctuation", func(t *testing.T) {
		_, paths := expandTags(root, "read (@a.go).")
		if len(paths) != 0 { // the "(" is part of the word, not a tag
			t.Errorf("paths = %v, want none", paths)
		}
		_, paths = expandTags(root, "read @a.go?")
		if want := []string{"a.go"}; !reflect.DeepEqual(paths, want) {
			t.Errorf("paths = %v, want %v", paths, want)
		}
	})

	t.Run("truncates big files", func(t *testing.T) {
		big := writeTree(t, map[string]string{"big.txt": strings.Repeat("x", maxTagBytes+10)})
		got, _ := expandTags(big, "@big.txt")
		if !strings.Contains(got, "[truncated]") || len(got) > maxTagBytes+200 {
			t.Errorf("big file not cut (len %d)", len(got))
		}
	})
}

func newTagApp(t *testing.T, onPrompt func(context.Context, string, func(Event)) error) *app {
	m, _ := newTestApp(onPrompt)
	m.root = writeTree(t, map[string]string{
		"main.go":            "package main\n",
		"internal/x.go":      "package x\n",
		"internal/y_test.go": "package x\n",
	})
	return m
}

func TestAtOpensFileMenu(t *testing.T) {
	m := newTagApp(t, nil)
	typeText(m, "see @int")
	if got, want := names(m.suggestions), []string{"internal/x.go", "internal/y_test.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("suggestions = %v, want %v", got, want)
	}
	if !m.suggestions[0].File {
		t.Error("suggestion should be a file")
	}

	typeText(m, "x")
	if got := names(m.suggestions); len(got) != 1 || got[0] != "internal/x.go" {
		t.Errorf("suggestions after narrowing = %v", got)
	}

	// Moving past the tag closes the menu.
	typeText(m, " ")
	if len(m.suggestions) != 0 {
		t.Errorf("menu should close after a space, got %v", names(m.suggestions))
	}
}

func TestTabAcceptsFile(t *testing.T) {
	m := newTagApp(t, nil)
	typeText(m, "see @mai")
	press(m, tea.KeyTab)
	if got, want := m.input.Value(), "see @main.go "; got != want {
		t.Errorf("value after tab = %q, want %q", got, want)
	}
	if len(m.suggestions) != 0 {
		t.Error("menu should close after accepting")
	}
}

func TestArrowsChooseFile(t *testing.T) {
	m := newTagApp(t, nil)
	typeText(m, "@int")
	press(m, tea.KeyDown)
	press(m, tea.KeyTab)
	if got, want := m.input.Value(), "@internal/y_test.go "; got != want {
		t.Errorf("value = %q, want %q", got, want)
	}
}

func TestEnterAcceptsPartialTagThenSubmits(t *testing.T) {
	got := make(chan string, 1)
	m := newTagApp(t, func(_ context.Context, text string, _ func(Event)) error {
		got <- text
		return nil
	})
	typeText(m, "explain @mai")
	press(m, tea.KeyEnter)
	if m.busy {
		t.Fatal("first enter should complete the tag, not submit")
	}
	if want := "explain @main.go "; m.input.Value() != want {
		t.Fatalf("value = %q, want %q", m.input.Value(), want)
	}

	cmd := press(m, tea.KeyEnter)
	if cmd == nil || !m.busy {
		t.Fatal("second enter should submit")
	}
	m.Update(func() tea.Msg { return doneMsg{err: m.opts.OnPrompt(m.ctx, "", nil)} }())
}

func TestEnterOnWholeTagSubmitsWithFileAttached(t *testing.T) {
	var sent string
	m := newTagApp(t, func(_ context.Context, text string, _ func(Event)) error {
		sent = text
		return nil
	})
	typeText(m, "explain @main.go")
	// The menu is open on the exact file, so enter submits.
	cmd := press(m, tea.KeyEnter)
	if cmd == nil || !m.busy {
		t.Fatal("enter on a whole tag should submit")
	}
	batch, ok := cmd().(tea.BatchMsg) // the prompt, then the spinner tick
	if !ok {
		t.Fatal("submit should batch the prompt with the spinner")
	}
	batch[0]()
	if want := "explain @main.go\n\n<file path=\"main.go\">\npackage main\n</file>"; sent != want {
		t.Errorf("model got %q, want %q", sent, want)
	}

	var transcript strings.Builder
	for _, e := range m.tr.entries {
		transcript.WriteString(e.text + "\n")
	}
	if !strings.Contains(transcript.String(), "attached main.go") {
		t.Errorf("transcript should note the attachment:\n%s", transcript.String())
	}
	if strings.Contains(transcript.String(), "<file") {
		t.Errorf("transcript should show the typed text, not the file block:\n%s", transcript.String())
	}
}
