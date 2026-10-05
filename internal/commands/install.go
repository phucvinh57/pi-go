package commands

import "github.com/spf13/cobra"

func newInstallCmd() *cobra.Command {
	var local bool

	cmd := &cobra.Command{
		Use:   "install <source>",
		Short: "Install an extension source and add it to settings",
		Example: `  pi-go install npm:@foo/bar
  pi-go install git:github.com/user/repo
  pi-go install ./local/path`,
		Args: cobra.ExactArgs(1),
		RunE: notImplemented,
	}

	cmd.Flags().BoolVarP(&local, "local", "l", false, "install project-locally (.pi-go/settings.json)")

	return cmd
}
