package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// AnnotationSlash set to "false" on a cobra command keeps it out of the
// interactive session: commands that open their own TUI or need a browser.
const AnnotationSlash = "pi.slash"

// slashEnabled reports whether a command may be run (and suggested) from the
// interactive session.
func slashEnabled(c *cobra.Command) bool {
	if c.Annotations[AnnotationSlash] == "false" {
		return false
	}
	switch c.Name() {
	case "help", "completion":
		return false
	}
	return c.IsAvailableCommand()
}

// runCobra runs argv through a fresh cobra tree and returns everything the
// command wrote to stdout and stderr. A fresh tree per call keeps flag values
// from one run from leaking into the next.
//
// The returned error is the command's own error. Cobra has already printed it
// into the output unless the command silenced it.
func runCobra(ctx context.Context, newCommand func() *cobra.Command, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("no command given")
	}

	root := newCommand()

	// An unknown name falls through to the root command, whose RunE would
	// start another interactive session inside this one.
	target, _, err := root.Find(argv)
	if err != nil {
		return "", err
	}
	if target == root {
		return "", fmt.Errorf("unknown command /%s (try /help)", argv[0])
	}
	for c := target; c != nil && c != root; c = c.Parent() {
		if !slashEnabled(c) {
			path := strings.TrimPrefix(target.CommandPath(), root.Name()+" ")
			return "", fmt.Errorf("/%s is not available in an interactive session; run `%s %s` in your shell", path, root.Name(), path)
		}
	}

	var buf bytes.Buffer
	root.SetArgs(argv)
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetIn(strings.NewReader(""))

	err = root.ExecuteContext(ctx)
	return buf.String(), err
}
