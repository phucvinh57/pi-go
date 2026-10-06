package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phucvinh57/pi-go/internal/ai"
	"github.com/phucvinh57/pi-go/internal/browser"
	"github.com/phucvinh57/pi-go/internal/config"
	"github.com/phucvinh57/pi-go/internal/session"
	"github.com/phucvinh57/pi-go/internal/tui"
)

var statsNow = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

// statsEnv gives the test an empty agent directory, a fixed clock and a browser
// that is never launched. The returned slice collects what it was asked to open.
func statsEnv(t *testing.T) (dir string, opened *[]string) {
	t.Helper()
	dir = t.TempDir()
	t.Setenv(config.AgentDirEnv, dir)

	oldNow := now
	now = func() time.Time { return statsNow }
	t.Cleanup(func() { now = oldNow })

	opened = new([]string)
	oldOpen := browser.Open
	browser.Open = func(target string) error { *opened = append(*opened, target); return nil }
	t.Cleanup(func() { browser.Open = oldOpen })
	return dir, opened
}

// saveSession writes a session of one prompt and one billed reply, started at
// start in cwd.
func saveSession(t *testing.T, dir, cwd string, start time.Time, in, out int, cost float64) {
	t.Helper()
	tick := start
	w := session.NewWriter(dir, cwd, func() time.Time { tick = tick.Add(time.Second); return tick })
	w.Record(ai.UserText("explain the build"))
	w.Record(ai.Message{
		Role: ai.RoleAssistant, Provider: "ollama", Model: "qwen", StopReason: ai.StopStop,
		Content: []ai.Block{{Type: ai.BlockText, Text: "it compiles"}},
		Usage:   ai.Usage{Input: in, Output: out, TotalTokens: in + out, Cost: ai.Cost{Total: cost}},
	})
	if err := w.Err(); err != nil {
		t.Fatal(err)
	}
	w.Close()
}

func runStats(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newStatsCmd()
	var o, e bytes.Buffer
	cmd.SetOut(&o)
	cmd.SetErr(&e)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return o.String(), e.String(), err
}

func TestStatsJSON(t *testing.T) {
	dir, opened := statsEnv(t)
	cwd, _ := os.Getwd()
	saveSession(t, dir, cwd, statsNow.Add(-2*time.Hour), 1000, 200, 0.5)
	saveSession(t, dir, "/elsewhere", statsNow.Add(-26*time.Hour), 300, 50, 0.25)

	stdout, _, err := runStats(t, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var sum session.Summary
	if err := json.Unmarshal([]byte(stdout), &sum); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	ov := sum.Overview
	if ov.Sessions != 2 || ov.Prompts != 2 || ov.Calls != 2 || ov.Tokens.Input != 1300 || ov.Tokens.Output != 250 || ov.TotalTokens != 1550 {
		t.Errorf("overview = %+v", ov)
	}
	if ov.Cost != 0.75 || len(sum.Models) != 1 || sum.Models[0].Ref != "ollama/qwen" || len(sum.Projects) != 2 {
		t.Errorf("summary = %+v", sum)
	}
	if len(*opened) != 0 {
		t.Errorf("--json opened %v", *opened)
	}
	if _, err := os.Stat(filepath.Join(dir, "stats.html")); err == nil {
		t.Error("--json wrote a page")
	}
}

func TestStatsWritesAndOpensThePage(t *testing.T) {
	dir, opened := statsEnv(t)
	saveSession(t, dir, "/work/app", statsNow.Add(-time.Hour), 1000, 200, 0.5)

	stdout, _, err := runStats(t)
	if err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(dir, "stats.html")
	if len(*opened) != 1 || (*opened)[0] != page {
		t.Errorf("opened %v, want [%s]", *opened, page)
	}
	if !strings.Contains(stdout, "1 sessions · 1 model calls · 1200 tokens · $0.5000") || !strings.Contains(stdout, "Wrote "+page) {
		t.Errorf("stdout = %q", stdout)
	}

	html, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"pi-go usage", "ollama/qwen", "/work/app", "explain the build", "Tokens per day"} {
		if !strings.Contains(string(html), want) {
			t.Errorf("the page is missing %q", want)
		}
	}
	info, _ := os.Stat(page)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v: the page quotes prompts, so it is private", info.Mode().Perm())
	}
	// No temp file is left beside it.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".stats-") {
			t.Errorf("left %s behind", e.Name())
		}
	}
}

func TestStatsOutAndNoOpen(t *testing.T) {
	dir, opened := statsEnv(t)
	saveSession(t, dir, "/p", statsNow.Add(-time.Hour), 10, 5, 0)

	target := filepath.Join(t.TempDir(), "nested", "dir", "report.html")
	stdout, _, err := runStats(t, "--out", target, "--no-open")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("not written: %v", err)
	}
	if len(*opened) != 0 {
		t.Errorf("--no-open opened %v", *opened)
	}
	if !strings.Contains(stdout, "Wrote "+target) {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestStatsSaysWhenTheBrowserCannotOpen(t *testing.T) {
	dir, _ := statsEnv(t)
	browser.Open = func(string) error { return errors.New("no xdg-open") }
	saveSession(t, dir, "/p", statsNow.Add(-time.Hour), 10, 5, 0)

	stdout, _, err := runStats(t)
	if err != nil {
		t.Fatalf("a missing browser must not fail the command: %v", err)
	}
	if !strings.Contains(stdout, "Could not open a browser (no xdg-open)") || !strings.Contains(stdout, "stats.html") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestStatsSince(t *testing.T) {
	dir, _ := statsEnv(t)
	saveSession(t, dir, "/p", statsNow.Add(-2*time.Hour), 100, 10, 0)
	saveSession(t, dir, "/p", statsNow.Add(-40*24*time.Hour), 5000, 500, 0)

	count := func(args ...string) int {
		t.Helper()
		stdout, _, err := runStats(t, append([]string{"--json"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		var sum session.Summary
		if err := json.Unmarshal([]byte(stdout), &sum); err != nil {
			t.Fatal(err)
		}
		return sum.Overview.Sessions
	}
	if got := count(); got != 2 {
		t.Errorf("all = %d sessions", got)
	}
	if got := count("--since", "7d"); got != 1 {
		t.Errorf("7d = %d sessions", got)
	}
	if got := count("--since", "6w"); got != 2 {
		t.Errorf("6w = %d sessions", got)
	}
	if got := count("--since", "2026-10-08"); got != 1 {
		t.Errorf("since the 8th = %d sessions", got)
	}
}

func TestStatsHere(t *testing.T) {
	dir, _ := statsEnv(t)
	cwd, _ := os.Getwd()
	saveSession(t, dir, cwd, statsNow.Add(-time.Hour), 100, 10, 0)
	saveSession(t, dir, "/elsewhere", statsNow.Add(-time.Hour), 7, 7, 0)

	stdout, _, err := runStats(t, "--json", "--here")
	if err != nil {
		t.Fatal(err)
	}
	var sum session.Summary
	json.Unmarshal([]byte(stdout), &sum)
	if sum.Overview.Sessions != 1 || sum.CWD != cwd || sum.Overview.Tokens.Input != 100 {
		t.Errorf("summary = %+v", sum.Overview)
	}
}

func TestStatsWithoutSessions(t *testing.T) {
	dir, opened := statsEnv(t)

	stdout, _, err := runStats(t)
	if err != nil {
		t.Fatalf("nothing to show is not an error: %v", err)
	}
	if !strings.Contains(stdout, "No sessions found under "+filepath.Join(dir, "sessions")) {
		t.Errorf("stdout = %q", stdout)
	}
	if len(*opened) != 0 {
		t.Error("opened a browser for nothing")
	}
	if _, err := os.Stat(filepath.Join(dir, "stats.html")); err == nil {
		t.Error("wrote an empty page")
	}

	// Sessions exist, but none in the period: say so, differently.
	saveSession(t, dir, "/p", statsNow.Add(-90*24*time.Hour), 1, 1, 0)
	stdout, _, err = runStats(t, "--since", "7d")
	if err != nil || !strings.Contains(stdout, "No sessions match this period and project") {
		t.Errorf("stdout = %q, err %v", stdout, err)
	}

	// And --json still answers, with zeros.
	stdout, _, err = runStats(t, "--json")
	var sum session.Summary
	if err != nil || json.Unmarshal([]byte(stdout), &sum) != nil {
		t.Fatalf("--json with no sessions: %v %q", err, stdout)
	}
}

func TestStatsWarnsAboutUnreadableFilesButShowsTheRest(t *testing.T) {
	dir, _ := statsEnv(t)
	saveSession(t, dir, "/p", statsNow.Add(-time.Hour), 100, 10, 0)
	bad := filepath.Join(session.Dir(dir, "/p"), "broken.jsonl")
	if err := os.WriteFile(bad, []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runStats(t, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "warning") || !strings.Contains(stderr, "broken.jsonl") {
		t.Errorf("stderr = %q", stderr)
	}
	var sum session.Summary
	if err := json.Unmarshal([]byte(stdout), &sum); err != nil || sum.Overview.Sessions != 1 {
		t.Errorf("stdout must stay clean JSON with the good session: %v\n%s", err, stdout)
	}
}

func TestStatsRejectsBadInput(t *testing.T) {
	statsEnv(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--since", "yesterday"}, `invalid --since "yesterday"`},
		{[]string{"--since", "0d"}, "at least 1"},
		{[]string{"--since", "-3d"}, "invalid --since"},
		{[]string{"--json", "--out", "x.html"}, "cannot be combined"},
		{[]string{"--json", "--no-open"}, "cannot be combined"},
		{[]string{"extra"}, "unknown command"},
	} {
		_, _, err := runStats(t, c.args...)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: err = %v, want it to mention %q", c.args, err, c.want)
		}
	}
}

func TestParseSince(t *testing.T) {
	base := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Time{
		"":    {},
		"all": {},
		"7d":  base.AddDate(0, 0, -7),
		"90d": base.AddDate(0, 0, -90),
		"2w":  base.AddDate(0, 0, -14),
		"1d":  base.AddDate(0, 0, -1),
	} {
		got, err := parseSince(in, base)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseSince(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	got, err := parseSince("2026-10-01", base)
	if err != nil || got.Year() != 2026 || got.Month() != 10 || got.Day() != 1 || got.Hour() != 0 {
		t.Errorf("a date = %v, %v", got, err)
	}
	for _, bad := range []string{"d", "7", "7x", "1.5d", "2026-13-01", "next week"} {
		if _, err := parseSince(bad, base); err == nil {
			t.Errorf("parseSince(%q) should fail", bad)
		}
	}
}

// It opens a browser, so the interactive session must refuse it as a slash
// command, and it must be registered.
func TestStatsIsRegisteredAndNotASlashCommand(t *testing.T) {
	var found bool
	for _, c := range All() {
		if c.Name() == "stats" {
			found = true
			if c.Annotations[tui.AnnotationSlash] != "false" {
				t.Error("stats must be hidden from slash commands")
			}
		}
	}
	if !found {
		t.Error("stats is not in commands.All")
	}
}
