package tui

import (
	"sort"
	"strings"
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
// It completes command names only, not flags. cmds may be nil.
func suggest(cmds Commands, input string) []suggestion {
	if !strings.HasPrefix(input, "/") || strings.Contains(input, "\n") {
		return nil
	}

	words := strings.Split(input[1:], " ")
	prefix, done := words[len(words)-1], nonEmpty(words[:len(words)-1])

	var out []suggestion
	if len(done) == 0 {
		for name, b := range builtins {
			if !b.hidden && strings.HasPrefix(name, prefix) {
				out = append(out, suggestion{Name: name, Desc: b.desc})
			}
		}
	}

	if cmds != nil {
		children, ok := cmds.List(done)
		if !ok {
			return nil
		}
		lead := strings.Join(done, " ")
		for _, c := range children {
			if strings.HasPrefix(c.Name, prefix) {
				name := c.Name
				if lead != "" {
					name = lead + " " + name
				}
				out = append(out, suggestion{Name: name, Desc: c.Desc})
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
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
