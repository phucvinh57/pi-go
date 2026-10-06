package tui

import (
	"context"
	"strings"
)

// fakeCommands is a tree of `echo` and `grp sub`, enough to drive the slash
// menu and the bridge. It records what it was asked to run.
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
