// Package cli defines the pi-go command-line interface.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"pi-go/internal/tui"
)

// Version is overridden at build time with -ldflags "-X pi-go/internal/cli.Version=...".
var Version = "dev"

// New builds the root `pi-go` command with the subcommands from subcommands
// attached. subcommands is called once per tree: the interactive session builds
// a fresh tree for every slash command (cobra keeps flag values between runs).
func New(subcommands func() []*cobra.Command) *cobra.Command {
	var newTree func() *cobra.Command
	newTree = func() *cobra.Command {
		root := newRootCmd(newTree)
		root.AddCommand(subcommands()...)
		return root
	}
	return newTree()
}

func newRootCmd(newTree func() *cobra.Command) *cobra.Command {
	var print bool

	cmd := &cobra.Command{
		Use:   "pi-go [messages...]",
		Short: "A Go clone of the PI coding agent",
		Long: "A Go clone of the PI coding agent.\n\n" +
			"In a terminal, pi-go opens an interactive session; messages become the first prompt.\n" +
			"With -p, or when stdin or stdout is not a terminal, pi-go answers once and exits.",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: false,
		// Positional args are messages, not subcommand names.
		Args: cobra.ArbitraryArgs,
	}

	cmd.Flags().BoolVarP(&print, "print", "p", false, "run once and print the answer instead of opening a session")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		stdinTTY := isTerminal(cmd.InOrStdin())
		stdoutTTY := isTerminal(cmd.OutOrStdout())
		prompt := strings.Join(args, " ")

		if chooseMode(print, stdinTTY, stdoutTTY) == modeInteractive {
			return tui.Run(cmd.Context(), tui.Options{
				InitialPrompt: prompt,
				NewCommand:    newTree,
				OnPrompt:      respond,
			})
		}

		if !stdinTTY {
			piped, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return fmt.Errorf("read stdin: %w", err)
			}
			prompt = joinPrompt(string(piped), prompt)
		}
		reply, err := respond(cmd.Context(), prompt)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), reply)
		return nil
	}

	return cmd
}

// respond answers one prompt. It is the seam where the agent session plugs in
// (see CHECKLIST.md, phases 1-7).
func respond(_ context.Context, prompt string) (string, error) {
	return fmt.Sprintf("pi-go %s: prompt=%q", Version, prompt), nil
}

// joinPrompt puts piped input before the command-line message, as PI does.
func joinPrompt(piped, arg string) string {
	piped = strings.TrimSpace(piped)
	switch {
	case piped == "":
		return arg
	case arg == "":
		return piped
	}
	return piped + "\n\n" + arg
}

func isTerminal(v any) bool {
	f, ok := v.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}
