package commands

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"pi-go/internal/auth"
)

// errNotReady makes `auth check` exit non-zero. The results are already
// printed, so the error itself is not.
var errNotReady = errors.New("provider is not ready")

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Log in to providers and check readiness",
	}

	cmd.AddCommand(newAuthLoginCmd(), newAuthCheckCmd())

	return cmd
}

func newAuthLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login <provider>",
		Short: "Log in to a provider with an API key or OAuth",
		Long: "Log in to a provider and save the credential to auth.json.\n\n" +
			"Providers:\n  " + strings.Join(auth.LoginMethods(), "\n  ") + "\n\n" +
			"API-key providers read the key from the terminal (not echoed) or from stdin.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("choose a provider to log in to:\n  %s\nusage: pi-go auth login <provider>",
					strings.Join(auth.LoginMethods(), "\n  "))
			}
			provider := args[0]
			method, err := auth.LoginMethodOf(provider)
			if err != nil {
				return err
			}

			if method == "oauth" {
				err = auth.LoginOAuth(cmd.Context(), provider, cmd.InOrStdin(), cmd.ErrOrStderr())
			} else {
				var key string
				if key, err = readSecret(cmd, fmt.Sprintf("Enter API key for %s: ", provider)); err == nil {
					err = auth.SaveAPIKey(provider, key)
				}
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Logged in to %s; credentials saved to %s\n",
				provider, filepath.Join(auth.AgentDir(), "auth.json"))
			return nil
		},
	}
}

// readSecret prompts on stderr and reads one line. From a terminal the input
// is not echoed; from a pipe it is read as plain text.
func readSecret(cmd *cobra.Command, prompt string) (string, error) {
	fmt.Fprint(cmd.ErrOrStderr(), prompt)
	if f, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(cmd.ErrOrStderr())
		return string(b), err
	}
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no API key provided")
	}
	return line, nil
}

func newAuthCheckCmd() *cobra.Command {
	var (
		provider string
		model    string
		asJSON   bool
	)

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check whether providers are ready to use",
		Long: "Check whether providers are ready to use.\n\n" +
			"Without --provider or --model, every supported provider is checked.\n" +
			"Exits non-zero if any checked provider is not ready.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			targets := auth.Supported()
			if provider != "" || model != "" {
				id, err := auth.ResolveProvider(provider, model)
				if err != nil {
					return err
				}
				targets = []string{id}
			}

			statuses := make([]auth.Status, 0, len(targets))
			allReady := true
			for _, id := range targets {
				status := auth.Check(cmd.Context(), id)
				statuses = append(statuses, status)
				allReady = allReady && status.Ready
			}

			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(statuses); err != nil {
					return err
				}
			} else {
				for _, s := range statuses {
					if s.Ready {
						fmt.Fprintf(out, "%s: ready (%s)\n", s.Provider, s.Source)
					} else {
						fmt.Fprintf(out, "%s: not ready: %s\n", s.Provider, s.Error)
					}
				}
			}

			if !allReady {
				cmd.SilenceErrors = true
				return errNotReady
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&provider, "provider", "", "check only this provider (supported: "+strings.Join(auth.Supported(), ", ")+")")
	cmd.Flags().StringVar(&model, "model", "", "check only the provider this model belongs to")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the results as a JSON array")

	return cmd
}
