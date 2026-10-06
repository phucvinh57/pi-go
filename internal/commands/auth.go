package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/phucvinh57/pi-go/internal/auth"
	"github.com/phucvinh57/pi-go/internal/config"
	"github.com/phucvinh57/pi-go/internal/prompt"
	"github.com/phucvinh57/pi-go/internal/slashcmd"
)

// errNotReady makes `auth check` exit non-zero. The results are already
// printed, so the error itself is not.
var errNotReady = errors.New("provider is not ready")

// newAuthCmd builds the auth command.
func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Log in to and out of providers, and check readiness",
	}

	cmd.AddCommand(newAuthLoginCmd(), newAuthLogoutCmd(), newAuthCheckCmd())

	return cmd
}

// authStore is the credential store of the agent directory. A command makes it
// when it runs, so a test (or the user) can point the directory elsewhere.
func authStore() *auth.Store { return auth.NewStore(config.AgentDir()) }

// prompterFor returns who to ask questions of, and whether that can include
// choosing from a list. The interactive session supplies its own through the
// context; in a plain terminal questions are drawn inline; otherwise answers
// are read as lines from stdin and nothing can be chosen.
func prompterFor(cmd *cobra.Command) (p prompt.Prompter, canChoose bool) {
	if p := prompt.From(cmd.Context()); p != nil {
		return p, true
	}
	in, errOut := cmd.InOrStdin(), cmd.ErrOrStderr()
	if prompt.Interactive(in, errOut) {
		return prompt.Terminal{In: in, Out: errOut}, true
	}
	return prompt.NewLines(in, errOut), false
}

// chooseProvider lets the user pick one of the supported providers. extra
// options are listed first; the returned index counts them, so a result below
// len(extra) is one of them. The provider is "" in that case.
func chooseProvider(cmd *cobra.Command, p prompt.Prompter, title string, extra ...string) (idx int, provider string, err error) {
	ids := auth.Supported()
	options := append(append([]string{}, extra...), auth.LoginMethods()...)
	idx, err = p.Select(cmd.Context(), title, options)
	if err != nil {
		return 0, "", err
	}
	if idx < len(extra) {
		return idx, "", nil
	}
	return idx, ids[idx-len(extra)], nil
}

func newAuthLoginCmd() *cobra.Command {
	var provider string

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to a provider with an API key or OAuth",
		Long: "Log in to a provider and save the credential to auth.json.\n\n" +
			"Without --provider, a terminal session lets you choose one from a list.\n\n" +
			"Providers:\n  " + strings.Join(auth.LoginMethods(), "\n  ") + "\n\n" +
			"API keys are typed or pasted (not echoed), or read from stdin when it is not a terminal.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store := authStore()
			p, canChoose := prompterFor(cmd)
			if provider == "" {
				if !canChoose {
					return fmt.Errorf("choose a provider to log in to:\n  %s\nusage: pi-go auth login --provider <provider>",
						strings.Join(auth.LoginMethods(), "\n  "))
				}
				var err error
				if _, provider, err = chooseProvider(cmd, p, "Log in to:"); err != nil {
					return err
				}
			}
			method, err := auth.LoginMethodOf(provider)
			if err != nil {
				return err
			}

			if method == "oauth" {
				paste := func(ctx context.Context) (string, error) {
					return p.Input(ctx, "Redirect URL:", false)
				}
				err = store.LoginOAuth(cmd.Context(), provider, paste, cmd.ErrOrStderr())
			} else {
				var key string
				if key, err = p.Input(cmd.Context(), "Enter API key for "+provider+":", true); errors.Is(err, io.EOF) {
					err = errors.New("no API key provided")
				}
				if err == nil {
					err = store.SaveAPIKey(provider, key)
				}
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Logged in to %s; credentials saved to %s\n",
				provider, store.AuthPath())
			return nil
		},
	}

	cmd.Flags().StringVar(&provider, "provider", "", "provider to log in to (supported: "+strings.Join(auth.Supported(), ", ")+")")
	cmd.Flags().SetAnnotation("provider", slashcmd.AnnotationValues, auth.Supported())

	return cmd
}

func newAuthLogoutCmd() *cobra.Command {
	var provider string

	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove a provider's saved credential",
		Long: "Remove a provider's credential from auth.json.\n\n" +
			"Providers that need no real key (ollama) are marked as logged out, so they\n" +
			"are not used or listed until you log in again.\n\n" +
			"Without --provider, a terminal session lets you choose one from a list.\n" +
			"Credentials from environment variables or models.json are not affected.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store := authStore()
			if provider == "" {
				p, canChoose := prompterFor(cmd)
				if !canChoose {
					return fmt.Errorf("choose a provider to log out of:\n  %s\nusage: pi-go auth logout --provider <provider>",
						strings.Join(auth.LoginMethods(), "\n  "))
				}
				var err error
				if _, provider, err = chooseProvider(cmd, p, "Log out of:"); err != nil {
					return err
				}
			}

			removed, err := store.Logout(provider)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if !removed {
				fmt.Fprintf(out, "Not logged in to %s: nothing to remove from %s\n",
					provider, store.AuthPath())
				return nil
			}
			fmt.Fprintf(out, "Logged out of %s; logged out in %s\n",
				provider, store.AuthPath())
			return nil
		},
	}

	cmd.Flags().StringVar(&provider, "provider", "", "provider to log out of (supported: "+strings.Join(auth.Supported(), ", ")+")")
	cmd.Flags().SetAnnotation("provider", slashcmd.AnnotationValues, auth.Supported())

	return cmd
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
			"Without --provider or --model, every supported provider is checked;\n" +
			"a terminal session first lets you choose one instead (not with --json).\n" +
			"Exits non-zero if any checked provider is not ready.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store := authStore()
			targets := auth.Supported()
			if p, canChoose := prompterFor(cmd); canChoose && provider == "" && model == "" && !asJSON {
				const all = "all providers"
				idx, chosen, err := chooseProvider(cmd, p, "Check:", all)
				if err != nil {
					return err
				}
				if idx > 0 {
					targets = []string{chosen}
				}
			} else if provider != "" || model != "" {
				id, err := store.ResolveProvider(provider, model)
				if err != nil {
					return err
				}
				targets = []string{id}
			}

			statuses := make([]auth.Status, 0, len(targets))
			allReady := true
			for _, id := range targets {
				status := store.Check(cmd.Context(), id)
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
	cmd.Flags().SetAnnotation("provider", slashcmd.AnnotationValues, auth.Supported())
	cmd.Flags().StringVar(&model, "model", "", "check only the provider this model belongs to")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the results as a JSON array")

	return cmd
}
