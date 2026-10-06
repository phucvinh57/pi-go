// Package report renders a session.Summary as one self-contained HTML page: no
// scripts from elsewhere, no fonts, no network, so it opens from a file and
// works offline. The charts are inline SVG drawn here, which keeps the page
// small and the numbers testable.
package report

import (
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/phucvinh57/pi-go/internal/session"
)

//go:embed report.html
var pageHTML string

var tmpl = template.Must(template.New("report").Funcs(template.FuncMap{
	"n":      func(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) },
	"tokens": formatTokens,
	"money":  money,
	"num":    groupDigits,
	"pct":    func(v float64) string { return fmt.Sprintf("%.1f%%", v) },
}).Parse(pageHTML))

// Options tune how a report is rendered.
type Options struct {
	// Loc is the time zone times are shown in; nil means time.Local.
	Loc *time.Location
}

type tile struct{ Label, Value, Sub string }

type dayRow struct {
	Date                     string
	Sessions, Prompts, Calls int
	Uncached, Cached, Output int
	Cost                     float64
}

type recentRow struct {
	Started, Project, ProjectFull, FirstPrompt, Models, Path string
	Calls, Tokens                                            int
	Cost                                                     float64
}

type page struct {
	Window, Project, Generated string
	S                          session.Summary
	Empty                      bool
	EmptyText                  string
	Tiles                      []tile
	Tokens                     *stackedChart
	Cost                       *lineChart
	CostNote                   string
	Models                     *barChart
	Days                       []dayRow // newest first
	Recent                     []recentRow
	SubscriptionCost           float64
}

// Render writes the report for s to w.
func Render(w io.Writer, s session.Summary, opt Options) error {
	return tmpl.Execute(w, build(s, opt))
}

func build(s session.Summary, opt Options) page {
	loc := opt.Loc
	if loc == nil {
		loc = time.Local
	}
	p := page{
		S:                s,
		Window:           "All time",
		Project:          "All projects",
		Generated:        s.GeneratedAt.In(loc).Format("Jan 2, 2006 15:04"),
		SubscriptionCost: s.Overview.SubscriptionCost,
	}
	if !s.Since.IsZero() {
		p.Window = "Since " + s.Since.In(loc).Format("Jan 2, 2006")
	}
	if s.CWD != "" {
		p.Project = s.CWD
	}

	if s.Overview.Sessions == 0 {
		p.Empty = true
		p.EmptyText = "No sessions yet. Conversations are saved as you use pi-go, and show up here."
		if !s.Since.IsZero() || s.CWD != "" {
			p.EmptyText = "No sessions match this period and project."
		}
		return p
	}

	p.Tiles = tiles(s.Overview)
	p.Tokens = newStackedChart(s.Days)
	p.Cost = newLineChart(s.Days)
	if p.Cost == nil {
		p.CostNote = "Nothing has a price yet. Add a cost (dollars per million tokens) to a model in models.json to see what it would cost."
	} else {
		p.CostNote = p.Cost.Note
	}
	p.Models = newBarChart(s.Models)

	for i := len(s.Days) - 1; i >= 0; i-- {
		d := s.Days[i]
		p.Days = append(p.Days, dayRow{
			Date: d.Date, Sessions: d.Sessions, Prompts: d.Prompts, Calls: d.Calls,
			Uncached: d.Tokens.Input + d.Tokens.CacheWrite, Cached: d.Tokens.CacheRead, Output: d.Tokens.Output,
			Cost: d.Cost,
		})
	}
	for _, r := range s.Recent {
		p.Recent = append(p.Recent, recentRow{
			Started:     r.Start.In(loc).Format("Jan 2 15:04"),
			Project:     filepath.Base(r.CWD),
			ProjectFull: r.CWD,
			FirstPrompt: r.FirstPrompt,
			Models:      strings.Join(r.Models, ", "),
			Path:        r.Path,
			Calls:       r.Calls, Tokens: r.TotalTokens, Cost: r.Cost,
		})
	}
	return p
}

func tiles(ov session.Overview) []tile {
	calls := tile{Label: "Model calls", Value: groupDigits(ov.Calls)}
	if ov.FailedCalls > 0 {
		calls.Sub = fmt.Sprintf("%d failed", ov.FailedCalls)
	}

	cost := tile{Label: "Cost", Value: money(ov.Cost)}
	switch {
	case ov.SubscriptionCost > 0:
		cost.Sub = fmt.Sprintf("%s on a subscription, not billed", money(ov.SubscriptionCost))
	case ov.Cost == 0 && ov.Calls > 0:
		cost.Sub = "no prices set"
	}

	hit := tile{Label: "Cache hit", Value: "–", Sub: "no prompts"}
	if ov.CacheHit >= 0 {
		hit.Value = fmt.Sprintf("%.1f%%", ov.CacheHit)
		hit.Sub = "of prompt tokens"
	}

	return []tile{
		{Label: "Sessions", Value: groupDigits(ov.Sessions)},
		{Label: "Prompts", Value: groupDigits(ov.Prompts)},
		calls,
		{
			Label: "Tokens", Value: formatTokens(ov.TotalTokens),
			Sub: fmt.Sprintf("%s in · %s out", formatTokens(ov.Tokens.Prompt()), formatTokens(ov.Tokens.Output)),
		},
		cost,
		hit,
	}
}

// groupDigits writes n with thousands separators: 1,234,567.
func groupDigits(n int) string {
	s := strconv.Itoa(n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 && s[i-1] != '-' {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// WriteFile renders the page to path. The file is private: it holds the
// opening words of the user's prompts and the paths of their projects. It is
// written through a temporary file, so a failure never leaves half a page.
func WriteFile(path string, sum session.Summary) error {
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
	if err := Render(tmp, sum, Options{}); err != nil {
		tmp.Close()
		return fmt.Errorf("render the page: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
