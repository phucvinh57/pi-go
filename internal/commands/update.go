package commands

import "github.com/spf13/cobra"

func newUpdateCmd() *cobra.Command {
	var all, extensions, models bool

	cmd := &cobra.Command{
		Use:   "update [source|self|pi]",
		Short: "Update pi, extensions, or model catalogs",
		Long: `Update pi, installed extensions, or model catalogs.

With no target and no flags, only pi itself is updated.`,
		Args: cobra.MaximumNArgs(1),
		RunE: notImplemented,
	}

	cmd.Flags().BoolVar(&all, "all", false, "update pi and installed extensions")
	cmd.Flags().BoolVar(&extensions, "extensions", false, "update installed extensions only")
	cmd.Flags().BoolVar(&models, "models", false, "refresh model catalogs only")

	return cmd
}
