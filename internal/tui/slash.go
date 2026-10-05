package tui

import (
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// builtin is a slash command handled by the TUI itself rather than cobra.
type builtin struct {
	desc   string
	hidden bool // aliases: runnable but not suggested
	run    func(m *model, args []string) tea.Cmd
}

var builtins map[string]builtin

func init() {
	// Assigned in init: /help reads the table, so a literal initializer would
	// be an initialization cycle.
	builtins = map[string]builtin{
		"quit":  {desc: "Exit pi-go", run: runQuit},
		"exit":  {desc: "Exit pi-go", hidden: true, run: runQuit},
		"clear": {desc: "Clear the screen", run: runClear},
		"help":  {desc: "List available commands", run: runHelp},
	}
}

func runQuit(m *model, _ []string) tea.Cmd {
	m.quitting = true
	return tea.Quit
}

func runClear(*model, []string) tea.Cmd {
	return tea.ClearScreen
}

func runHelp(m *model, _ []string) tea.Cmd {
	var b strings.Builder
	b.WriteString("Built-in:\n")
	names := make([]string, 0, len(builtins))
	for name, bi := range builtins {
		if !bi.hidden {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		b.WriteString(row("/"+name, builtins[name].desc))
	}

	b.WriteString("\nCommands (same as `pi-go <command>` in the shell):\n")
	for _, c := range m.opts.NewCommand().Commands() {
		if slashEnabled(c) {
			b.WriteString(row("/"+c.Name(), c.Short))
		}
	}
	return m.print(strings.TrimRight(b.String(), "\n"))
}

func row(name, desc string) string {
	return "  " + name + strings.Repeat(" ", max(1, 12-len(name))) + desc + "\n"
}
