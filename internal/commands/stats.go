package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/phucvinh57/pi-go/internal/auth"
	"github.com/phucvinh57/pi-go/internal/browser"
	"github.com/phucvinh57/pi-go/internal/config"
	"github.com/phucvinh57/pi-go/internal/session"
	"github.com/phucvinh57/pi-go/internal/session/report"
	"github.com/phucvinh57/pi-go/internal/tui"
)

// now is the clock; tests replace it.
var now = time.Now

// newStatsCmd builds the stats command: a page of everything the saved sessions
// say about token use, cost and tool use.
func newStatsCmd() *cobra.Command {
	var (
		since  string
		here   bool
		out    string
		noOpen bool
		asJSON bool
	)

	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show token usage and cost across all saved sessions",
		Long: "Read the sessions pi-go saved and write one HTML page with the totals: tokens, cost,\n" +
			"cache use, and the busiest days, models, projects and tools. The page is a single file\n" +
			"that works offline, and nothing is sent anywhere.\n\n" +
			"--since takes all, a number of days (30d) or weeks (2w), or a date (2026-10-01).\n" +
			"Sessions started with --no-session are not saved, so they do not appear.",
		Example: "  pi-go stats\n  pi-go stats --since 30d --here\n  pi-go stats --json --since 7d",
		Args:    cobra.NoArgs,
		// It opens a browser, which a slash command in the session cannot.
		Annotations: map[string]string{tui.AnnotationSlash: "false"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if asJSON && (out != "" || noOpen) {
				return fmt.Errorf("--json prints the summary instead of writing a page, so it cannot be combined with --out or --no-open")
			}
			cutoff, err := parseSince(since, now())
			if err != nil {
				return err
			}

			filter := session.Filter{Since: cutoff, Subscription: auth.IsSubscription, Now: now()}
			if here {
				if filter.CWD, err = os.Getwd(); err != nil {
					return fmt.Errorf("working directory: %w", err)
				}
			}

			dir := config.AgentDir()
			sessions, loadErr := session.LoadAll(dir)
			if loadErr != nil {
				// Some files could not be read; the rest are still worth showing.
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: some session files were skipped:\n%v\n", loadErr)
			}
			sum := session.Summarize(sessions, filter)

			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(sum)
			}

			if sum.Overview.Sessions == 0 {
				switch {
				case len(sessions) == 0:
					fmt.Fprintf(cmd.OutOrStdout(), "No sessions found under %s yet.\n", filepath.Join(dir, "sessions"))
				default:
					fmt.Fprintln(cmd.OutOrStdout(), "No sessions match this period and project.")
				}
				return nil
			}

			if out == "" {
				out = filepath.Join(dir, "stats.html")
			}
			if err := writeReport(out, sum); err != nil {
				return err
			}

			ov := sum.Overview
			fmt.Fprintf(cmd.OutOrStdout(), "%d sessions · %d model calls · %d tokens · $%.4f\nWrote %s\n",
				ov.Sessions, ov.Calls, ov.TotalTokens, ov.Cost, out)

			if noOpen {
				return nil
			}
			if err := browser.Open(out); err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Could not open a browser (%v); open the file yourself.\n", err)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&since, "since", "all", "only count the period since then: all, 30d, 2w, or a date like 2026-10-01")
	cmd.Flags().BoolVar(&here, "here", false, "only count sessions started in the current directory")
	cmd.Flags().StringVar(&out, "out", "", "where to write the page (default: stats.html in the agent directory)")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "write the page but do not open it in a browser")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the numbers as JSON instead of writing a page")
	return cmd
}

// writeReport renders the page to path. The file is private: it holds the
// opening words of the user's prompts and the paths of their projects.
func writeReport(path string, sum session.Summary) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".stats-*.html")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := report.Render(tmp, sum, report.Options{}); err != nil {
		tmp.Close()
		return fmt.Errorf("render the page: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

var sinceSpan = regexp.MustCompile(`^(\d+)([dw])$`)

// parseSince turns the --since value into a cutoff; the zero time means all.
func parseSince(s string, now time.Time) (time.Time, error) {
	if s == "" || s == "all" {
		return time.Time{}, nil
	}
	if m := sinceSpan.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil || n <= 0 {
			return time.Time{}, fmt.Errorf("invalid --since %q: the number must be at least 1", s)
		}
		if m[2] == "w" {
			n *= 7
		}
		return now.AddDate(0, 0, -n), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid --since %q: use all, a number of days (30d) or weeks (2w), or a date (2026-10-01)", s)
}
