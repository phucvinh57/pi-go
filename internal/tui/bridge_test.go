package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// testTree returns a factory for a root with `echo`, `quiet` (hidden from
// slash) and `grp sub`. rootRan counts runs of the root's own RunE, which
// must never happen from a slash command.
func testTree(rootRan *int) func() *cobra.Command {
	return func() *cobra.Command {
		root := &cobra.Command{
			Use:          "pi-go [messages...]",
			SilenceUsage: true,
			Args:         cobra.ArbitraryArgs,
			RunE: func(*cobra.Command, []string) error {
				*rootRan++
				return nil
			},
		}

		var asJSON bool
		echo := &cobra.Command{
			Use:   "echo",
			Short: "Echo args",
			RunE: func(cmd *cobra.Command, args []string) error {
				if asJSON {
					cmd.Println("json:", strings.Join(args, "|"))
				} else {
					cmd.Println("text:", strings.Join(args, "|"))
				}
				return nil
			},
		}
		echo.Flags().BoolVar(&asJSON, "json", false, "")

		quiet := &cobra.Command{
			Use:         "quiet",
			Annotations: map[string]string{AnnotationSlash: "false"},
			RunE:        func(*cobra.Command, []string) error { return nil },
		}

		grp := &cobra.Command{Use: "grp"}
		grp.AddCommand(&cobra.Command{Use: "sub", Short: "Sub", RunE: func(*cobra.Command, []string) error { return nil }})
		grp.AddCommand(&cobra.Command{
			Use:         "shell",
			Annotations: map[string]string{AnnotationSlash: "false"},
			RunE:        func(*cobra.Command, []string) error { return nil },
		})

		root.AddCommand(echo, quiet, grp)
		return root
	}
}

func TestRunCobraCapturesOutput(t *testing.T) {
	var ran int
	out, err := runCobra(context.Background(), testTree(&ran), []string{"echo", "a", "b c"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "text: a|b c\n" {
		t.Errorf("output = %q", out)
	}
}

func TestRunCobraFreshTreePerRun(t *testing.T) {
	var ran int
	newTree := testTree(&ran)

	out, _ := runCobra(context.Background(), newTree, []string{"echo", "--json"})
	if !strings.HasPrefix(out, "json:") {
		t.Fatalf("first run = %q", out)
	}
	out, _ = runCobra(context.Background(), newTree, []string{"echo"})
	if !strings.HasPrefix(out, "text:") {
		t.Errorf("flag leaked into the second run: %q", out)
	}
}

func TestRunCobraRefuses(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want string
	}{
		{"unknown", []string{"nope"}, "unknown command /nope"},
		{"flag only", []string{"--help"}, "unknown command"},
		{"empty", nil, "no command"},
		{"annotated", []string{"quiet"}, "not available"},
		{"annotated subcommand", []string{"grp", "shell"}, "not available"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ran int
			_, err := runCobra(context.Background(), testTree(&ran), tt.argv)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to contain %q", err, tt.want)
			}
			if ran != 0 {
				t.Error("the root command ran: a slash command would nest a session")
			}
		})
	}
}
