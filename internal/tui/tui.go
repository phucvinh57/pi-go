package tui

import (
	"context"
	"fmt"
	"io"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"
)

// Options configures an interactive session.
type Options struct {
	// InitialPrompt, if not empty, is submitted as soon as the session opens.
	InitialPrompt string

	// NewCommand returns a fresh root command tree. It is called once per
	// slash command, because cobra keeps flag values between executions.
	NewCommand func() *cobra.Command

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
}

// Models is how the session shows and changes the model that answers prompts.
// Models are named "provider/id".
type Models interface {
	Current() string
	// Choices lists the models that can be selected. It may return models
	// together with an error describing the providers that could not be listed.
	Choices(ctx context.Context) ([]string, error)
	// Select makes ref the active model, or fails and keeps the current one.
	Select(ctx context.Context, ref string) error
}

// Run opens the interactive session, full screen, and blocks until the user
// quits. The conversation is then written to opts.Out.
func Run(ctx context.Context, opts Options) error {
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
