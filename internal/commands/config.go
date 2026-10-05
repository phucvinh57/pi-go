package commands

import (
	"github.com/spf13/cobra"

	"pi-go/internal/tui"
)

func newConfigCmd() *cobra.Command {
	var local bool

	cmd := &cobra.Command{
		Use:   "config",
		Short: "Open a TUI to enable or disable package resources",
		Args:  cobra.NoArgs,
		RunE:  notImplemented,
		// Runs its own TUI, which cannot nest inside a session.
		Annotations: map[string]string{tui.AnnotationSlash: "false"},
	}

	cmd.Flags().BoolVarP(&local, "local", "l", false, "edit project overrides (.pi-go/settings.json)")

	return cmd
}
