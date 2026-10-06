package slashcmd

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
		var format string
		echo.Flags().StringVar(&format, "format", "", "Output format")
		echo.Flags().SetAnnotation("format", AnnotationValues, []string{"text", "table"})

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

func TestRunCapturesOutput(t *testing.T) {
	var ran int
	out, err := New(testTree(&ran)).Run(context.Background(), []string{"echo", "a", "b c"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "text: a|b c\n" {
		t.Errorf("output = %q", out)
	}
}

func TestRunFreshTreePerRun(t *testing.T) {
	var ran int
	newTree := testTree(&ran)

	out, _ := New(newTree).Run(context.Background(), []string{"echo", "--json"})
	if !strings.HasPrefix(out, "json:") {
		t.Fatalf("first run = %q", out)
	}
	out, _ = New(newTree).Run(context.Background(), []string{"echo"})
	if !strings.HasPrefix(out, "text:") {
		t.Errorf("flag leaked into the second run: %q", out)
	}
}

func TestRunRefuses(t *testing.T) {
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
			_, err := New(testTree(&ran)).Run(context.Background(), tt.argv)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to contain %q", err, tt.want)
			}
			if ran != 0 {
				t.Error("the root command ran: a slash command would nest a session")
			}
		})
	}
}

func TestListHidesCommandsTheSessionCannotRun(t *testing.T) {
	var ran int
	c := New(testTree(&ran))

	top, ok := c.List(nil)
	if !ok {
		t.Fatal("top level should be listed")
	}
	var names []string
	for _, i := range top {
		names = append(names, i.Name)
	}
	if got := strings.Join(names, ","); got != "echo,grp" {
		t.Errorf("top level = %s, want echo,grp (quiet is annotated)", got)
	}

	sub, ok := c.List([]string{"grp"})
	if !ok || len(sub) != 1 || sub[0].Name != "sub" {
		t.Errorf("grp = %v, %v; want only sub", sub, ok)
	}
	for _, path := range [][]string{{"nope"}, {"quiet"}, {"grp", "shell"}} {
		if _, ok := c.List(path); ok {
			t.Errorf("List(%v) should not be usable", path)
		}
	}
}

func TestComplete(t *testing.T) {
	var ran int
	c := New(testTree(&ran))

	tests := []struct {
		words   []string
		partial string
		want    string // names joined by ","
		ok      bool
	}{
		{nil, "", "echo,grp", true},
		{nil, "e", "echo", true},
		{nil, "-", "", true}, // the root's flags start a session
		{[]string{"grp"}, "", "sub", true},
		{[]string{"echo"}, "--", "--format,--help,--json", true},
		{[]string{"echo"}, "--j", "--json", true},
		{[]string{"echo", "--json"}, "--", "--format,--help", true}, // given once already
		{[]string{"echo", "--format"}, "t", "text,table", true},
		{[]string{"echo", "--format"}, "ta", "table", true},
		{[]string{"echo"}, "--format=te", "--format=text", true},
		{[]string{"echo", "--format", "text"}, "", "", true},
		{[]string{"echo", "x"}, "", "", true},
		{[]string{"nope"}, "", "", false},
		{[]string{"quiet"}, "", "", false},
		{[]string{"grp", "shell"}, "", "", false},
	}
	for _, tt := range tests {
		t.Run(strings.Join(append(tt.words, tt.partial), " "), func(t *testing.T) {
			infos, ok := c.Complete(tt.words, tt.partial)
			var names []string
			for _, i := range infos {
				names = append(names, i.Name)
			}
			if got := strings.Join(names, ","); got != tt.want || ok != tt.ok {
				t.Errorf("Complete = %q, %v; want %q, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
	if ran != 0 {
		t.Error("completing ran the root command")
	}
}
