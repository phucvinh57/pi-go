package tui

import (
	"context"
	"strings"
)

// fakeCommands is a tree of `echo [--json]` and `grp sub`, enough to drive the
// slash menu and the bridge. It records what it was asked to run.
type fakeCommands struct {
	ran [][]string
}

func (f *fakeCommands) Run(_ context.Context, argv []string) (string, error) {
	f.ran = append(f.ran, argv)
	return "text: " + strings.Join(argv[1:], "|") + "\n", nil
}

func (f *fakeCommands) List(path []string) ([]CommandInfo, bool) {
	switch strings.Join(path, " ") {
	case "":
		return []CommandInfo{{Name: "echo", Desc: "Echo args"}, {Name: "grp"}}, true
	case "grp":
		return []CommandInfo{{Name: "sub", Desc: "Sub"}}, true
	case "echo":
		return nil, true
	}
	return nil, false
}

func (f *fakeCommands) Complete(words []string, partial string) ([]CommandInfo, bool) {
	var all []CommandInfo
	switch {
	case len(words) == 0 || len(words) == 1 && words[0] == "grp":
		all, _ = f.List(words)
	case words[0] == "echo":
		all = []CommandInfo{{Name: "--json", Desc: "As JSON"}}
	default:
		return nil, false
	}
	var out []CommandInfo
	for _, c := range all {
		if strings.HasPrefix(c.Name, partial) {
			out = append(out, c)
		}
	}
	return out, true
}
