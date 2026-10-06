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
	"github.com/spf13/pflag"
)

// AnnotationSlash set to "false" on a cobra command keeps it out of the
// interactive session: commands that open their own TUI or need a browser.
const AnnotationSlash = "pi.slash"

// AnnotationValues on a flag lists the values the session offers for it, set
// with cmd.Flags().SetAnnotation. Cobra's RegisterFlagCompletionFunc is not
// used: it keeps every flag it is given in a global map, and the session builds
// a fresh tree for every completion.
const AnnotationValues = "pi.values"

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

// Complete returns the candidates for partial, the word being typed after
// words: subcommands, flags (once partial starts with "-"), the values a flag
// lists in AnnotationValues, and a command's ValidArgs. Each Name replaces
// partial whole. The bool is false when words do not lead to a command that
// can be used from the session.
func (c *Commands) Complete(words []string, partial string) ([]Info, bool) {
	root := c.newTree()
	cmd, rest := root, words
	for len(rest) > 0 {
		child := childNamed(cmd, rest[0])
		if child == nil {
			break
		}
		if !slashEnabled(child) {
			return nil, false
		}
		cmd, rest = child, rest[1:]
	}
	if cmd == root && len(words) > 0 {
		return nil, false // the root's own arguments are messages, not commands
	}

	cmd.InitDefaultHelpFlag()
	cmd.InheritedFlags() // merges the parents' persistent flags into cmd.Flags()
	args, pending := scan(cmd.Flags(), rest)
	switch {
	case pending != nil:
		return withPrefix(flagValues(pending), partial, ""), true
	case strings.HasPrefix(partial, "--") && strings.Contains(partial, "="):
		name, value, _ := strings.Cut(partial[2:], "=")
		f := cmd.Flags().Lookup(name)
		if f == nil {
			return nil, true
		}
		return withPrefix(flagValues(f), value, "--"+name+"="), true
	case strings.HasPrefix(partial, "-"):
		if cmd == root {
			return nil, true // the root's flags start a session, not a command
		}
		return withPrefix(flagNames(cmd.Flags(), rest), partial, ""), true
	}

	var out []Info
	if len(args) == 0 {
		for _, sub := range cmd.Commands() {
			if slashEnabled(sub) {
				out = append(out, Info{Name: sub.Name(), Desc: sub.Short})
			}
		}
	}
	if cmd != root {
		for _, v := range cmd.ValidArgs {
			name, desc, _ := strings.Cut(v, "\t")
			out = append(out, Info{Name: name, Desc: desc})
		}
	}
	return withPrefix(out, partial, ""), true
}

// scan splits words into positional arguments and flags. pending is the flag
// whose value is the next word, when words end before it.
func scan(flags *pflag.FlagSet, words []string) (args []string, pending *pflag.Flag) {
	for i := 0; i < len(words); i++ {
		w := words[i]
		if w == "--" {
			return append(args, words[i+1:]...), nil
		}
		if len(w) < 2 || w[0] != '-' {
			args = append(args, w)
			continue
		}
		f := lookupFlag(flags, w)
		if f == nil || f.NoOptDefVal != "" || strings.Contains(w, "=") {
			continue // the value, if any, is in w
		}
		if i == len(words)-1 {
			return args, f
		}
		i++ // skip the value
	}
	return args, nil
}

// lookupFlag finds the flag w names, as --name or -n (with or without
// "=value").
func lookupFlag(flags *pflag.FlagSet, w string) *pflag.Flag {
	name, _, _ := strings.Cut(w, "=")
	if long, ok := strings.CutPrefix(name, "--"); ok {
		return flags.Lookup(long)
	}
	if len(name) == 2 {
		return flags.ShorthandLookup(name[1:])
	}
	return nil
}

// flagNames lists the flags as --name, leaving out the hidden ones and those
// already given in words unless they may be repeated.
func flagNames(flags *pflag.FlagSet, words []string) []Info {
	used := map[*pflag.Flag]bool{}
	for _, w := range words {
		if f := lookupFlag(flags, w); f != nil {
			used[f] = true
		}
	}
	var out []Info
	flags.VisitAll(func(f *pflag.Flag) {
		if f.Hidden || used[f] && !repeatable(f) {
			return
		}
		out = append(out, Info{Name: "--" + f.Name, Desc: f.Usage})
	})
	return out
}

func repeatable(f *pflag.Flag) bool {
	t := f.Value.Type()
	return strings.HasSuffix(t, "Slice") || strings.HasSuffix(t, "Array") || t == "count"
}

func flagValues(f *pflag.Flag) []Info {
	var out []Info
	for _, v := range f.Annotations[AnnotationValues] {
		out = append(out, Info{Name: v})
	}
	return out
}

// withPrefix keeps the candidates that start with prefix, with lead put before
// each name.
func withPrefix(infos []Info, prefix, lead string) []Info {
	var out []Info
	for _, in := range infos {
		if strings.HasPrefix(in.Name, prefix) {
			in.Name = lead + in.Name
			out = append(out, in)
		}
	}
	return out
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
