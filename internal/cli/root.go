// Package cli defines the pi-go command-line interface.
package cli

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/phucvinh57/pi-go/internal/slashcmd"
	"github.com/phucvinh57/pi-go/internal/tui"
)

// Version is overridden at build time with -ldflags "-X github.com/phucvinh57/pi-go/internal/cli.Version=...".
// When it is not, a binary from `go install ...@vX.Y.Z` reports the module
// version recorded in its build info.
var Version = buildVersion()

func buildVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

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
	var (
		print           bool
		noSession       bool
		provider, model string
	)

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
	cmd.Flags().BoolVar(&noSession, "no-session", false, "do not save this conversation (it will not appear in `pi-go stats`)")
	cmd.Flags().StringVar(&provider, "provider", "", "provider to use (default: taken from --model)")
	cmd.Flags().StringVar(&model, "model", "", `model to use, as "provider/id" or a bare ID (default: the saved default model, else `+DefaultModel+`)`)

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		stdinTTY := isTerminal(cmd.InOrStdin())
		stdoutTTY := isTerminal(cmd.OutOrStdout())
		prompt := strings.Join(args, " ")
		env := hostEnvironment()
		sess := newSession(env, provider, model)
		sess.conv.noSession = noSession

		if chooseMode(print, stdinTTY, stdoutTTY) == modeInteractive {
			return tui.Run(cmd.Context(), tui.Options{
				InitialPrompt: prompt,
				Version:       Version,
				Out:           cmd.OutOrStdout(),
				Commands:      commandsOf(newTree),
				Cwd:           env.cwd,
				Home:          env.home,
				OnPrompt:      sess.Prompt,
				Models:        sess,
				Effort:        sess,
				Plan:          sess,
			})
		}

		if !stdinTTY {
			piped, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return fmt.Errorf("read stdin: %w", err)
			}
			prompt = joinPrompt(string(piped), prompt)
		}
		reply, err := sess.Respond(cmd.Context(), prompt)
		if warning := sess.SaveError(); warning != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "warning:", warning)
		}
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), reply)
		return nil
	}

	return cmd
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

// commandsOf lets the interactive session run the shell's commands. It adapts
// slashcmd to tui.Commands, which share no types so that tui need not know
// cobra or slashcmd.
func commandsOf(newTree func() *cobra.Command) tui.Commands {
	return slashCommands{slashcmd.New(newTree)}
}

type slashCommands struct{ *slashcmd.Commands }

func (c slashCommands) List(path []string) ([]tui.CommandInfo, bool) {
	infos, ok := c.Commands.List(path)
	return commandInfos(infos), ok
}

func (c slashCommands) Complete(words []string, partial string) ([]tui.CommandInfo, bool) {
	infos, ok := c.Commands.Complete(words, partial)
	return commandInfos(infos), ok
}

func commandInfos(infos []slashcmd.Info) []tui.CommandInfo {
	out := make([]tui.CommandInfo, len(infos))
	for i, in := range infos {
		out[i] = tui.CommandInfo{Name: in.Name, Desc: in.Desc}
	}
	return out
}
