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
		"quit":    {desc: "Exit pi-go", run: runQuit},
		"exit":    {desc: "Exit pi-go", hidden: true, run: runQuit},
		"clear":   {desc: "Clear the conversation view", run: runClear},
		"help":    {desc: "List available commands", run: runHelp},
		"model":   {desc: "Pick the model (/model provider/id; --default also saves it)", run: runModel},
		"session": {desc: "Show token usage and cost for this session", run: runSession},
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

// runSession prints the session's usage from the latest snapshot the agent
// sent, so it works while a prompt is running and never waits for the agent.
func runSession(m *app, _ []string) tea.Cmd {
	text := "No usage yet: nothing has been sent to the model."
	if m.stats != nil {
		text = m.stats.report()
	}
	m.addEntry(entry{kind: kindInfo, text: text})
	return nil
}

func (m *app) fail(text string) tea.Cmd {
	m.addEntry(entry{kind: kindError, text: text})
	return nil
}

// runModel opens the model picker, or with an argument switches straight to it.
// With --default the chosen model is also saved as the default for future
// sessions.
func runModel(m *app, args []string) tea.Cmd {
	if m.opts.Models == nil {
		return m.fail("error: no model can be chosen in this session")
	}
	asDefault := false
	rest := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--default" {
			asDefault = true
		} else {
			rest = append(rest, a)
		}
	}
	switch len(rest) {
	case 0:
		return m.backgroundMsg(func(ctx context.Context) tea.Msg {
			items, err := m.opts.Models.Choices(ctx)
			return choicesMsg{items: items, err: err, asDefault: asDefault}
		})
	case 1:
		return m.selectModel(rest[0], asDefault)
	}
	return m.fail("usage: /model [--default] [provider/id]")
}

func (m *app) selectModel(ref string, asDefault bool) tea.Cmd {
	if asDefault {
		if err := m.opts.Models.SetDefault(m.ctx, ref); err != nil {
			return m.fail("error: " + err.Error())
		}
		m.current = m.opts.Models.Current()
		m.addEntry(entry{kind: kindNotice, text: "Model set to " + m.current + " (saved as default)"})
		return nil
	}
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
