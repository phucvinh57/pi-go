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

// suggest returns the completions for input, which is the whole editor text:
// command names, then their arguments (subcommands, flags and values).
func (m *app) suggest(input string) []suggestion {
	if !strings.HasPrefix(input, "/") || strings.Contains(input, "\n") {
		return nil
	}

	words := strings.Split(input[1:], " ")
	partial, done := words[len(words)-1], nonEmpty(words[:len(words)-1])
	cmds := m.opts.Commands

	var found []CommandInfo
	if len(done) == 0 {
		for name, b := range builtins {
			if !b.hidden && strings.HasPrefix(name, partial) {
				found = append(found, CommandInfo{Name: name, Desc: b.desc})
			}
		}
		if cmds != nil {
			infos, _ := cmds.Complete(nil, partial)
			found = append(found, infos...)
		}
		sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	} else if b, ok := builtins[done[0]]; ok {
		if b.complete != nil {
			for _, v := range b.complete(m, done[1:]) {
				if strings.HasPrefix(v, partial) {
					found = append(found, CommandInfo{Name: v})
				}
			}
		}
	} else if cmds != nil {
		found, _ = cmds.Complete(done, partial)
	}

	lead := strings.Join(done, " ")
	out := make([]suggestion, len(found))
	for i, f := range found {
		name := f.Name
		if lead != "" {
			name = lead + " " + name
		}
		out[i] = suggestion{Name: name, Desc: f.Desc}
	}
	return out
}

// completeModel offers --default, then the models to switch to.
func completeModel(m *app, args []string) []string {
	var out []string
	given := false
	for _, a := range args {
		if a == "--default" {
			given = true
		} else {
			return nil // the model is already named
		}
	}
	if !given {
		out = append(out, "--default")
	}
	return append(out, m.modelChoices()...)
}

// modelChoices returns the models /model can complete. The first call starts
// loading them in the background (listing a provider's models goes over the
// network) and returns none; a modelsMsg brings them in.
func (m *app) modelChoices() []string {
	if m.opts.Models == nil {
		return nil
	}
	if m.models == nil && !m.loadingModels {
		m.loadingModels = true
		go func() {
			items, _ := m.opts.Models.Choices(m.ctx) // errors show when /model opens the picker
			m.send(modelsMsg{items: items})
		}()
	}
	return m.models
}

func completeEffort(m *app, args []string) []string {
	if len(args) > 0 || m.opts.Effort == nil {
		return nil
	}
	return m.opts.Effort.Levels()
}

func completePlan(_ *app, args []string) []string {
	if len(args) > 0 {
		return nil
	}
	return []string{"on", "off"}
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
