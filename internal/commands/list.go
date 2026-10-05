package commands

import "github.com/spf13/cobra"

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List installed extensions from settings",
		Args:  cobra.NoArgs,
		RunE:  notImplemented,
	}
}
