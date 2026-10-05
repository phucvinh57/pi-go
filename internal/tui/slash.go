package tui

import (
	"context"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// builtin is a slash command handled by the TUI itself rather than cobra.
type builtin struct {
	desc   string
	hidden bool // aliases: runnable but not suggested
	run    func(m *app, args []string) tea.Cmd
}

var builtins map[string]builtin

func init() {
	// Assigned in init: /help reads the table, so a literal initializer would
	// be an initialization cycle.
	builtins = map[string]builtin{
		"quit":  {desc: "Exit pi-go", run: runQuit},
		"exit":  {desc: "Exit pi-go", hidden: true, run: runQuit},
		"clear": {desc: "Clear the conversation view", run: runClear},
		"help":  {desc: "List available commands", run: runHelp},
		"model": {desc: "Pick the model (or /model provider/id)", run: runModel},
	}
}

func runQuit(m *app, _ []string) tea.Cmd {
	// Keep the typed "/quit" out of the transcript printed on exit.
	if e := m.tr.last(); e != nil && e.kind == kindUser && (e.text == "/quit" || e.text == "/exit") {
		m.tr.entries = m.tr.entries[:len(m.tr.entries)-1]
	}
	m.quitting = true
	return tea.Quit
}

func runClear(m *app, _ []string) tea.Cmd {
	m.tr.clear()
	m.refresh(false)
	return nil
}

func (m *app) fail(text string) tea.Cmd {
	m.addEntry(entry{kind: kindError, text: text})
	return nil
}

// runModel opens the model picker, or with an argument switches straight to it.
func runModel(m *app, args []string) tea.Cmd {
	if m.opts.Models == nil {
		return m.fail("error: no model can be chosen in this session")
	}
	switch len(args) {
	case 0:
		return m.backgroundMsg(func(ctx context.Context) tea.Msg {
			items, err := m.opts.Models.Choices(ctx)
			return choicesMsg{items: items, err: err}
		})
	case 1:
		return m.selectModel(args[0])
	}
	return m.fail("usage: /model [provider/id]")
}

func (m *app) selectModel(ref string) tea.Cmd {
	if err := m.opts.Models.Select(m.ctx, ref); err != nil {
		return m.fail("error: " + err.Error())
	}
	m.current = m.opts.Models.Current()
	m.addEntry(entry{kind: kindNotice, text: "Model set to " + m.current})
	return nil
}

func runHelp(m *app, _ []string) tea.Cmd {
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
	m.addEntry(entry{kind: kindInfo, text: strings.TrimRight(b.String(), "\n")})
	return nil
}

func row(name, desc string) string {
	return "  " + name + strings.Repeat(" ", max(1, 12-len(name))) + desc + "\n"
}
