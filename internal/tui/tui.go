package tui

import (
	"context"
	"fmt"
	"io"

	tea "charm.land/bubbletea/v2"
)

// Options configures an interactive session.
type Options struct {
	// InitialPrompt, if not empty, is submitted as soon as the session opens.
	InitialPrompt string

	// Commands runs the slash commands that are not TUI built-ins (the same
	// commands as in the shell). If nil, only the built-ins exist.
	Commands Commands

	// Cwd is the working directory shown in the footer and the root for the
	// "@" file menu. Home, if it is a prefix of Cwd, is shown as "~".
	Cwd  string
	Home string

	// OnPrompt handles a line that is not a slash command. It reports the
	// reply and the tool calls through emit as they happen, and returns when
	// the prompt is finished. It runs off the UI goroutine; ctx is cancelled
	// when the user aborts.
	OnPrompt func(ctx context.Context, text string, emit func(Event)) error

	// Version is shown in the welcome banner.
	Version string

	// Out receives the conversation when the session ends, so it stays in the
	// terminal's scrollback after the full-screen view closes. Nil prints nothing.
	Out io.Writer

	// Models backs /model. If nil, /model reports that no model can be chosen.
	Models Models

	// Effort backs /effort. If nil, /effort reports that it cannot be set.
	Effort Effort

	// Plan backs /plan and shift+tab. If nil, plan mode cannot be turned on.
	Plan Plan

	// Clipboard receives text the user selects with the mouse. If nil, the
	// desktop clipboard is used.
	Clipboard Clipboard
}

// Commands is the set of commands a slash command can run besides the
// built-ins. The TUI knows nothing about how they are built.
type Commands interface {
	// Run runs argv (a command name, then its arguments) and returns what it
	// printed. The returned error is the command's own.
	Run(ctx context.Context, argv []string) (string, error)
	// List returns the commands directly under path (empty: the top level)
	// that can be used from the session. The bool is false when path does
	// not name such a command.
	List(path []string) ([]CommandInfo, bool)
	// Complete returns the candidates for partial, the word being typed after
	// words (a command path, then its arguments so far): subcommands, flags
	// and their values. Each Name replaces partial whole. The bool is false
	// when words do not lead to such a command.
	Complete(words []string, partial string) ([]CommandInfo, bool)
}

// CommandInfo is a command as the slash menu lists it.
type CommandInfo struct {
	Name string
	Desc string
}

// Models backs /model: the active model and the choices.
type Models interface {
	Current() string
	Choices(ctx context.Context) ([]string, error)
	Select(ctx context.Context, ref string) error
	SetDefault(ctx context.Context, ref string) error
}

// Effort is the reasoning effort of the model, as a level from Levels, or ""
// for the model's default.
type Effort interface {
	Effort() string
	Levels() []string
	SetEffort(level string) error
}

// Plan is the plan mode of the agent: while it is on, the model can only read
// and proposes a plan.
type Plan interface {
	PlanMode() bool
	SetPlanMode(on bool)
}

func Run(ctx context.Context, opts Options) error {
	if opts.Clipboard == nil {
		opts.Clipboard = systemClipboard{}
	}
	m := newApp(ctx, opts)
	p := tea.NewProgram(m, tea.WithContext(ctx))
	// Prompts run on their own goroutine and talk to the program through it.
	m.send = p.Send
	if _, err := p.Run(); err != nil {
		return err
	}
	if opts.Out != nil {
		if dump := m.tr.plain(m.width); dump != "" {
			fmt.Fprintln(opts.Out, dump)
		}
	}
	return nil
}
