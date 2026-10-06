// Package slashcmd runs the shell's cobra commands from the interactive
// session's slash commands. It is the only bridge between the two: the TUI
// sees it through its Commands interface and never imports cobra.
package slashcmd

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

// Info is a command as the session lists it.
type Info struct {
	Name string
	Desc string
}

// Commands is a cobra command tree, rebuilt for every run.
type Commands struct {
	// newTree returns a fresh root. Cobra keeps flag values between runs, so a
	// tree is never reused.
	newTree func() *cobra.Command
}

// New returns the commands of the tree newTree builds.
func New(newTree func() *cobra.Command) *Commands { return &Commands{newTree: newTree} }

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

// List returns the commands directly under path, in the tree's order; an empty
// path means the top level. The bool is false when path does not name a command
// that can be used from the session.
func (c *Commands) List(path []string) ([]Info, bool) {
	cmd := c.newTree()
	for _, w := range path {
		child := childNamed(cmd, w)
		if child == nil || !slashEnabled(child) {
			return nil, false
		}
		cmd = child
	}
	var out []Info
	for _, sub := range cmd.Commands() {
		if slashEnabled(sub) {
			out = append(out, Info{Name: sub.Name(), Desc: sub.Short})
		}
	}
	return out, true
}

func childNamed(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name || c.HasAlias(name) {
			return c
		}
	}
	return nil
}

// Run runs argv through a fresh cobra tree and returns everything the command
// wrote to stdout and stderr.
//
// The returned error is the command's own error. Cobra has already printed it
// into the output unless the command silenced it.
func (c *Commands) Run(ctx context.Context, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("no command given")
	}

	root := c.newTree()

	// An unknown name falls through to the root command, whose RunE would
	// start another interactive session inside this one.
	target, _, err := root.Find(argv)
	if err != nil {
		return "", err
	}
	if target == root {
		return "", fmt.Errorf("unknown command /%s (try /help)", argv[0])
	}
	for t := target; t != nil && t != root; t = t.Parent() {
		if !slashEnabled(t) {
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
