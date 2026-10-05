// Package tui is the interactive pi session: a Bubble Tea program that reads
// prompts and slash commands. Slash commands that are not TUI built-ins run
// through the same cobra tree as the shell (see bridge.go), so every
// subcommand is available as /<subcommand> without a second registry.
package tui

import (
	"context"

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

	// OnPrompt handles a line that is not a slash command and returns the
	// reply to show. It runs off the UI goroutine; ctx is cancelled when the
	// user aborts.
	OnPrompt func(ctx context.Context, text string) (string, error)
}

// Run opens the interactive session and blocks until the user quits.
func Run(ctx context.Context, opts Options) error {
	_, err := tea.NewProgram(newModel(ctx, opts), tea.WithContext(ctx)).Run()
	return err
}
