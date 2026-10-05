package tui

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// suggestion is one completion candidate: a slash command, or a file to tag.
type suggestion struct {
	// Name is the full replacement for the text after "/", e.g. "auth check",
	// or for a file the path to put after "@".
	Name string
	Desc string
	File bool
}

// suggest returns the completions for input, which is the whole editor text.
// It completes command names only, not flags.
func suggest(newCommand func() *cobra.Command, input string) []suggestion {
	if !strings.HasPrefix(input, "/") || strings.Contains(input, "\n") {
		return nil
	}

	words := strings.Split(input[1:], " ")
	prefix, done := words[len(words)-1], words[:len(words)-1]

	var out []suggestion
	if len(done) == 0 {
		for name, b := range builtins {
			if !b.hidden && strings.HasPrefix(name, prefix) {
				out = append(out, suggestion{Name: name, Desc: b.desc})
			}
		}
	}

	cmd := newCommand()
	for _, w := range done {
		if w == "" {
			continue // repeated spaces
		}
		child := childNamed(cmd, w)
		if child == nil || !slashEnabled(child) {
			return nil
		}
		cmd = child
	}
	if _, isBuiltin := builtins[firstWord(done)]; !isBuiltin {
		lead := strings.Join(nonEmpty(done), " ")
		for _, c := range cmd.Commands() {
			if slashEnabled(c) && strings.HasPrefix(c.Name(), prefix) {
				name := c.Name()
				if lead != "" {
					name = lead + " " + name
				}
				out = append(out, suggestion{Name: name, Desc: c.Short})
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
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

func firstWord(words []string) string {
	if w := nonEmpty(words); len(w) > 0 {
		return w[0]
	}
	return ""
}

func nonEmpty(words []string) []string {
	out := make([]string, 0, len(words))
	for _, w := range words {
		if w != "" {
			out = append(out, w)
		}
	}
	return out
}
