package commands

import (
	"github.com/spf13/cobra"

	"pi-go/internal/tui"
)

func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Configure MCP servers and sign in to OAuth servers",
		Long: `Configure and check MCP servers without starting a session.

Reads ~/.pi-go/agent/mcp.json and, in trusted projects, .pi-go/mcp.json.`,
	}

	cmd.AddCommand(
		newMCPAddCmd(),
		newMCPRemoveCmd(),
		newMCPListCmd(),
		newMCPLoginCmd(),
		newMCPLogoutCmd(),
	)

	return cmd
}

func newMCPAddCmd() *cobra.Command {
	var (
		local  bool
		url    string
		env    []string
		header []string
	)

	cmd := &cobra.Command{
		Use:   "add <server> [flags] (-- <command> [args...] | --url <url>)",
		Short: "Add or replace a server in mcp.json",
		Example: `  pi-go mcp add fs -- npx -y @modelcontextprotocol/server-filesystem .
  pi-go mcp add docs --url https://example.com/mcp`,
		Args: cobra.MinimumNArgs(1),
		RunE: notImplemented,
	}

	cmd.Flags().BoolVarP(&local, "local", "l", false, "use .pi-go/mcp.json in the current project")
	cmd.Flags().StringVar(&url, "url", "", "streamable HTTP server URL (instead of a command)")
	cmd.Flags().StringArrayVar(&env, "env", nil, "environment variable for a stdio server, KEY=VALUE (repeatable)")
	cmd.Flags().StringArrayVar(&header, "header", nil, "HTTP header, KEY=VALUE (repeatable)")

	return cmd
}

func newMCPRemoveCmd() *cobra.Command {
	var local bool

	cmd := &cobra.Command{
		Use:   "remove <server>",
		Short: "Remove a server from mcp.json",
		Args:  cobra.ExactArgs(1),
		RunE:  notImplemented,
	}

	cmd.Flags().BoolVarP(&local, "local", "l", false, "use .pi-go/mcp.json in the current project")

	return cmd
}

func newMCPListCmd() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "Show server state, tools, and errors (exits 1 on failure)",
		Args:  cobra.NoArgs,
		RunE:  notImplemented,
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "print the list as JSON")

	return cmd
}

func newMCPLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login <server>",
		Short: "Sign in to an OAuth server through the browser",
		Args:  cobra.ExactArgs(1),
		RunE:  notImplemented,
		// Opens a browser and waits for the callback: shell only.
		Annotations: map[string]string{tui.AnnotationSlash: "false"},
	}
}

func newMCPLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout <server>",
		Short: "Delete the stored OAuth credentials for a server",
		Args:  cobra.ExactArgs(1),
		RunE:  notImplemented,
	}
}
