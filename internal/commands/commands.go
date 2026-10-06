// Package commands holds the pi subcommands. Each command lives in its own
// file and exposes a constructor that returns a *cobra.Command.
package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

// All returns every pi subcommand, ready to be added to the root command.
func All() []*cobra.Command {
	return []*cobra.Command{
		newAuthCmd(),
		newStatsCmd(),
	}
}

// notImplemented is the placeholder RunE for commands that are not built yet.
func notImplemented(cmd *cobra.Command, _ []string) error {
	return fmt.Errorf("%s: not implemented", cmd.CommandPath())
}
