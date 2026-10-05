package commands

import "github.com/spf13/cobra"

func newRemoveCmd() *cobra.Command {
	var local bool

	cmd := &cobra.Command{
		Use:     "remove <source>",
		Aliases: []string{"uninstall"},
		Short:   "Remove an extension source from settings",
		Args:    cobra.ExactArgs(1),
		RunE:    notImplemented,
	}

	cmd.Flags().BoolVarP(&local, "local", "l", false, "remove from project settings (.pi-go/settings.json)")

	return cmd
}
