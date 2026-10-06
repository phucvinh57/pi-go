package report

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/phucvinh57/pi-go/internal/session"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// nearPx compares coordinates read back from SVG path data, which carries two
// decimals.
func nearPx(a, b float64) bool { return math.Abs(a-b) < 0.011 }

func TestNiceScale(t *testing.T) {
	for _, c := range []struct{ max, top, step float64 }{
		{0, 1, 1},
		{7, 8, 2},
		{100, 100, 25},
		{1234, 1500, 500},
		{4_000_000, 4_000_000, 1_000_000},
		{4500, 6000, 2000},
		{0.35, 0.4, 0.1},
	} {
		top, step := niceScale(c.max)
		if !near(top, c.top) || !near(step, c.step) {
			t.Errorf("niceScale(%v) = %v, %v; want %v, %v", c.max, top, step, c.top, c.step)
		}
		if top < c.max {
			t.Errorf("niceScale(%v): the axis tops out at %v, below the data", c.max, top)
		}
	}
}

func TestFormatting(t *testing.T) {
	for n, want := range map[int]string{999: "999", 1234: "1.2k", 45_600: "46k", 1_500_000: "1.5M"} {
		if got := formatTokens(n); got != want {
			t.Errorf("formatTokens(%d) = %q, want %q", n, got, want)
		}
	}
	for v, want := range map[float64]string{0: "0", 1000: "1k", 2500: "2.5k", 2_000_000: "2M"} {
		if got := axisTokens(v); got != want {
			t.Errorf("axisTokens(%v) = %q, want %q", v, got, want)
		}
	}
	for v, want := range map[float64]string{0: "$0", 0.00042: "$0.0004", 0.5: "$0.500", 1: "$1.00", 12.345: "$12.35"} {
		if got := money(v); got != want {
			t.Errorf("money(%v) = %q, want %q", v, got, want)
		}
	}
	for v, want := range map[float64]string{0: "$0", 1: "$1", 0.25: "$0.25", 0.5: "$0.5", 2.5: "$2.5"} {
		if got := axisMoney(v); got != want {
			t.Errorf("axisMoney(%v) = %q, want %q", v, got, want)
		}
	}
	if got := groupDigits(1234567); got != "1,234,567" {
		t.Errorf("groupDigits = %q", got)
	}
}

func day(date string, sessions, calls int, in, cacheRead, out int, cost float64) session.Day {
	return session.Day{
		Date: date, Sessions: sessions, Prompts: calls, Calls: calls,
		Tokens: session.Tokens{Input: in, CacheRead: cacheRead, Output: out}, Cost: cost,
	}
}

// bbox reads the y extent of a rectangle path made by rect or roundedTop.
func yExtent(t *testing.T, path string) (top, bottom float64) {
	t.Helper()
	var x, y, x2, y2 float64
	if strings.Contains(path, "Q") {
		// M x y+h V y+r Q ...
		var h float64
		if _, err := fmt.Sscanf(path, "M%f %f V%f", &x, &h, &y); err != nil {
			t.Fatalf("path %q: %v", path, err)
		}
		// y here is y+r; the real top is the first Q's control point.
		var qx, qy float64
		idx := strings.Index(path, "Q")
		if _, err := fmt.Sscanf(path[idx:], "Q%f %f", &qx, &qy); err != nil {
			t.Fatalf("path %q: %v", path, err)
		}
		return qy, h
	}
	if _, err := fmt.Sscanf(path, "M%f %f H%f V%f", &x, &y, &x2, &y2); err != nil {
		t.Fatalf("path %q: %v", path, err)
	}
	return y, y2
}

func TestStackedColumnSegments(t *testing.T) {
	c := newStackedChart([]session.Day{day("2026-10-04", 1, 3, 1000, 3000, 500, 0.5)})
	if len(c.Cols) != 1 || len(c.Cols[0].Segs) != 3 {
		t.Fatalf("columns = %+v", c.Cols)
	}
	segs := c.Cols[0].Segs
	if segs[0].Class != "s1" || segs[1].Class != "s2" || segs[2].Class != "s3" {
		t.Errorf("series order = %v %v %v, want s1 s2 s3 bottom to top", segs[0].Class, segs[1].Class, segs[2].Class)
	}

	top, _ := niceScale(4500)
	scale := (c.Y1 - c.Y0) / top

	// The bottom segment sits on the baseline; its end is square.
	y0, b0 := yExtent(t, segs[0].Path)
	if !nearPx(b0, c.Y1) || !nearPx(y0, c.Y1-1000*scale) || strings.Contains(segs[0].Path, "Q") {
		t.Errorf("bottom segment spans %v..%v, baseline %v", y0, b0, c.Y1)
	}
	// The next one is 2px shorter at the bottom: the gap between segments.
	y1, b1 := yExtent(t, segs[1].Path)
	if !nearPx(y1, c.Y1-4000*scale) || !nearPx(b1, y0-2) || strings.Contains(segs[1].Path, "Q") {
		t.Errorf("middle segment spans %v..%v, want to end 2px above %v", y1, b1, y0)
	}
	// Only the topmost segment has the rounded data end.
	if !strings.Contains(segs[2].Path, "Q") {
		t.Errorf("topmost segment is not rounded: %s", segs[2].Path)
	}
	y2, _ := yExtent(t, segs[2].Path)
	if !nearPx(y2, c.Y1-4500*scale) {
		t.Errorf("top of the column = %v, want %v", y2, c.Y1-4500*scale)
	}

	// With one day the slot is wide; the bar is capped, not stretched.
	var x, y, right float64
	if _, err := fmt.Sscanf(segs[0].Path, "M%f %f H%f", &x, &y, &right); err != nil {
		t.Fatal(err)
	}
	if got := right - x; !nearPx(got, maxBarW) {
		t.Errorf("bar width = %v, want the %vpx cap", got, maxBarW)
	}
	// ...and sits in the middle of the plot.
	if mid := (c.X0 + c.X1) / 2; !nearPx((x+right)/2, mid) {
		t.Errorf("bar is centred at %v, want %v", (x+right)/2, mid)
	}
}

func TestStackedColumnRoundsTheTopmostNonEmptySegment(t *testing.T) {
	// No output tokens: the cached segment is the top of the column.
	c := newStackedChart([]session.Day{day("2026-10-04", 1, 1, 1000, 2000, 0, 0)})
	segs := c.Cols[0].Segs
	if len(segs) != 2 || strings.Contains(segs[0].Path, "Q") || !strings.Contains(segs[1].Path, "Q") {
		t.Errorf("segments = %+v", segs)
	}
}

func TestStackedChartQuietDaysAndLimits(t *testing.T) {
	days := []session.Day{
		day("2026-10-04", 1, 1, 10, 0, 5, 0),
		{Date: "2026-10-05"},
		day("2026-10-06", 1, 1, 10, 0, 5, 0),
	}
	c := newStackedChart(days)
	if len(c.Cols) != 3 || len(c.Cols[1].Segs) != 0 {
		t.Fatalf("a quiet day keeps its slot and draws nothing: %+v", c.Cols[1])
	}
	if !strings.Contains(c.Cols[1].Tip, "No activity") || !strings.Contains(c.Cols[1].Tip, "Oct 5 2026") {
		t.Errorf("quiet day tip = %q", c.Cols[1].Tip)
	}
	if c.Cols[1].HitW <= 0 {
		t.Error("a quiet day still has a hover target")
	}
	if len(c.XTicks) != 3 || c.XTicks[0].Label != "Oct 4" {
		t.Errorf("x ticks = %+v", c.XTicks)
	}

	var many []session.Day
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 120; i++ {
		many = append(many, day(start.AddDate(0, 0, i).Format("2006-01-02"), 1, 1, 10, 0, 5, 0))
	}
	c = newStackedChart(many)
	if len(c.Cols) != maxDays || !strings.Contains(c.Note, "last 90 of 120") {
		t.Errorf("%d columns, note %q", len(c.Cols), c.Note)
	}
	if len(c.XTicks) > maxXLabel {
		t.Errorf("%d x labels, want at most %d", len(c.XTicks), maxXLabel)
	}
	for i, col := range c.Cols {
		if col.HitX < c.X0-1e-6 || col.HitX+col.HitW > c.X1+1e-6 {
			t.Fatalf("column %d hit target leaves the plot: %v+%v", i, col.HitX, col.HitW)
		}
	}
	if c.Cols[len(c.Cols)-1].Tip == "" || !strings.Contains(c.Cols[0].Tip, "Jan 31 2026") {
		t.Errorf("the chart must show the most recent days; first tip %q", c.Cols[0].Tip)
	}
}

func TestStackedChartTipHasTheNumbers(t *testing.T) {
	c := newStackedChart([]session.Day{{
		Date: "2026-10-04", Sessions: 2, Prompts: 3, Calls: 1,
		Tokens: session.Tokens{Input: 1000, CacheWrite: 200, CacheRead: 3000, Output: 500}, Cost: 0.42,
	}})
	tip := c.Cols[0].Tip
	for _, want := range []string{"Sun, Oct 4 2026", "2 sessions · 3 prompts · 1 call", "Uncached input 1.2k · Cached input 3.0k · Output 500", "$0.420"} {
		if !strings.Contains(tip, want) {
			t.Errorf("tip is missing %q:\n%s", want, tip)
		}
	}
}

func TestStackedChartEmpty(t *testing.T) {
	c := newStackedChart(nil)
	if len(c.Cols) != 0 || len(c.Legend) != 3 {
		t.Errorf("chart = %+v", c)
	}
}

func TestLineChart(t *testing.T) {
	if newLineChart([]session.Day{day("2026-10-04", 1, 1, 10, 0, 5, 0)}) != nil {
		t.Error("a chart of nothing but zeros should not be drawn")
	}
	if newLineChart(nil) != nil {
		t.Error("no days, no chart")
	}

	days := []session.Day{
		day("2026-10-04", 1, 1, 10, 0, 5, 0.25),
		day("2026-10-05", 1, 1, 10, 0, 5, 0),
		day("2026-10-06", 1, 1, 10, 0, 5, 1),
	}
	c := newLineChart(days)
	if c == nil {
		t.Fatal("no chart")
	}
	if len(c.Dots) != 3 || len(c.Hits) != 3 || c.End != c.Dots[2] {
		t.Errorf("dots %d, hits %d, end %v", len(c.Dots), len(c.Hits), c.End)
	}
	if c.EndLabel != "$1.00" || c.EndAnchor != "middle" {
		t.Errorf("end label %q anchored %q: with room around the last point it is centred on it", c.EndLabel, c.EndAnchor)
	}
	// The biggest day reaches the top tick; the zero day sits on the baseline.
	if !near(c.Dots[1].Y, c.Y1) || c.Dots[2].Y > c.Dots[0].Y {
		t.Errorf("dots = %+v", c.Dots)
	}
	if !strings.HasPrefix(c.Line, "M") || strings.Count(c.Line, "L") != 2 || !strings.HasSuffix(c.Area, "Z") {
		t.Errorf("line %q area %q", c.Line, c.Area)
	}
	if c.YTicks[0].Label != "$0" {
		t.Errorf("y ticks = %+v", c.YTicks)
	}
}

// With many days the last point is close to the right edge, so a centred label
// would run off the chart.
func TestLineChartEndLabelStaysInside(t *testing.T) {
	var days []session.Day
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 60; i++ {
		days = append(days, day(start.AddDate(0, 0, i).Format("2006-01-02"), 1, 1, 10, 0, 5, float64(i+1)))
	}
	c := newLineChart(days)
	if c.EndAnchor != "end" {
		t.Errorf("anchor = %q at x=%v of %v: want the label to end at the point", c.EndAnchor, c.End.X, c.X1)
	}
}

func TestBarChart(t *testing.T) {
	if newBarChart(nil) != nil {
		t.Error("no models, no chart")
	}

	var models []session.ModelStat
	for i := 0; i < 12; i++ {
		models = append(models, session.ModelStat{Ref: fmt.Sprintf("p/model-%02d", i), Calls: 1, TotalTokens: 1000 * (12 - i), Share: 100.0 / 12})
	}
	c := newBarChart(models)
	if len(c.Rows) != barRows+1 {
		t.Fatalf("%d rows, want %d and one for the rest", len(c.Rows), barRows)
	}
	other := c.Rows[len(c.Rows)-1]
	if other.Label != "Other (2 models)" || other.Value != "3.0k" { // 2000 + 1000
		t.Errorf("other row = %+v", other)
	}
	// The longest bar is the biggest model; bars are rounded at the data end only.
	if !strings.Contains(c.Rows[0].Path, "Q") || c.Rows[0].Value != "12k" {
		t.Errorf("first row = %+v", c.Rows[0])
	}
	if !(c.Rows[0].ValueX > c.Rows[3].ValueX && c.Rows[3].ValueX > other.ValueX) {
		t.Error("bar lengths must follow the values")
	}
	if c.H != float64(len(c.Rows))*30 {
		t.Errorf("height = %v", c.H)
	}

	long := newBarChart([]session.ModelStat{{Ref: strings.Repeat("x", 80), TotalTokens: 5}})
	if n := len([]rune(long.Rows[0].Label)); n != 36 || !strings.HasSuffix(long.Rows[0].Label, "…") {
		t.Errorf("long label is %d runes: %q", n, long.Rows[0].Label)
	}
	if !strings.Contains(long.Rows[0].Tip, strings.Repeat("x", 80)) {
		t.Error("the tooltip carries the full name")
	}

	zero := newBarChart([]session.ModelStat{{Ref: "a/b"}})
	if zero.Rows[0].Path != "" {
		t.Error("a model with no tokens has no bar")
	}
}

// The table is ordered by cost, but the bars by what they show.
func TestBarChartOrdersByTokens(t *testing.T) {
	c := newBarChart([]session.ModelStat{
		{Ref: "a/pricey", TotalTokens: 100, Cost: 9},
		{Ref: "b/big", TotalTokens: 900},
		{Ref: "c/mid", TotalTokens: 500, Cost: 1},
	})
	var got []string
	for _, r := range c.Rows {
		got = append(got, r.Label)
	}
	if strings.Join(got, " ") != "b/big c/mid a/pricey" {
		t.Errorf("rows = %v", got)
	}
}

func sampleSummary() session.Summary {
	return session.Summary{
		GeneratedAt: time.Date(2026, 10, 8, 14, 2, 0, 0, time.UTC),
		Overview: session.Overview{
			Sessions: 2, Prompts: 5, Calls: 9, FailedCalls: 1, ToolCalls: 4,
			Tokens:      session.Tokens{Input: 4000, Output: 900, CacheRead: 16000, CacheWrite: 100},
			TotalTokens: 21000, Cost: 2.85, SubscriptionCost: 2.1, CacheHit: 79.6,
		},
		Days: []session.Day{
			day("2026-10-04", 1, 4, 3000, 8000, 500, 0.75),
			day("2026-10-06", 1, 5, 1100, 8100, 400, 2.1),
		},
		Models: []session.ModelStat{
			{Ref: "openai-codex/gpt-5.5", Provider: "openai-codex", Calls: 5, FailedCalls: 1, TotalTokens: 12000, Cost: 2.1, Subscription: true, Share: 57.1},
			{Ref: "ollama/qwen", Provider: "ollama", Calls: 4, TotalTokens: 9000, Cost: 0.75, Share: 42.9},
		},
		Projects: []session.ProjectStat{{CWD: "/work/app", Sessions: 2, Calls: 9, TotalTokens: 21000, Cost: 2.85}},
		Tools:    []session.ToolStat{{Name: "bash", Calls: 3, Errors: 1}, {Name: "read", Calls: 1}},
		Recent: []session.SessionStat{{
			ID: "abc", Start: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC), CWD: "/work/app",
			Path: "/home/me/.pi-go/agent/sessions/--work-app--/x.jsonl", Models: []string{"ollama/qwen", "openai-codex/gpt-5.5"},
			FirstPrompt: "fix the parser", Calls: 5, TotalTokens: 12000, Cost: 2.1,
		}},
	}
}

func render(t *testing.T, s session.Summary) string {
	t.Helper()
	var b bytes.Buffer
	if err := Render(&b, s, Options{Loc: time.UTC}); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestRenderShowsTheNumbers(t *testing.T) {
	out := render(t, sampleSummary())
	for _, want := range []string{
		"<title>pi-go usage</title>",
		"All time · All projects · generated Oct 8, 2026 14:02",
		">Sessions<", ">Model calls<", "1 failed",
		">21k<",   // the tokens tile
		">$2.85<", // the cost tile
		"$2.10 on a subscription, not billed",
		"79.6%", // cache hit
		"Tokens per day", "Cost per day", "Tokens by model", "Projects", "Tools", "Recent sessions",
		"Uncached input", "Cached input", ">Output<", // the legend
		"openai-codex/gpt-5.5", "(sub)", "ollama/qwen", "57.1%",
		"/work/app", "fix the parser", "Oct 6 09:00",
		"/home/me/.pi-go/agent/sessions/--work-app--/x.jsonl",
		"prefers-color-scheme: dark",
		"Includes $2.10 from subscription models",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the page is missing %q", want)
		}
	}

	if open, closed := strings.Count(out, "<svg"), strings.Count(out, "</svg>"); open != 3 || closed != 3 {
		t.Errorf("%d <svg> and %d </svg>, want 3 each (tokens, cost, models)", open, closed)
	}
}

func TestRenderIsSelfContained(t *testing.T) {
	out := render(t, sampleSummary())
	for _, bad := range []string{"http://", "https://", "src=", "<link", "@import", "url("} {
		if strings.Contains(out, bad) {
			t.Errorf("the page reaches outside itself: found %q", bad)
		}
	}
	if n := strings.Count(out, "<script"); n != 1 {
		t.Errorf("%d scripts, want just the inline tooltip", n)
	}
}

func TestRenderEscapesWhatTheUserTyped(t *testing.T) {
	s := sampleSummary()
	evil := `</td><script>alert(1)</script>"><img src=x onerror=alert(2)>`
	s.Recent[0].FirstPrompt = evil
	s.Recent[0].CWD = evil
	s.Recent[0].Path = evil
	s.Projects[0].CWD = evil
	s.Models[0].Ref = evil
	s.Tools[0].Name = evil
	s.CWD = evil
	out := render(t, s)

	if strings.Contains(out, "<script>alert") || strings.Contains(out, "<img src=x") || strings.Contains(out, `onerror=alert(2)>`) {
		t.Fatal("user text reached the page unescaped")
	}
	if n := strings.Count(out, "<script"); n != 1 {
		t.Errorf("%d script tags after injection, want 1", n)
	}
	if !strings.Contains(out, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Error("the text is not shown, escaped")
	}
}

func TestTooltipsKeepTheirLineBreaks(t *testing.T) {
	out := render(t, sampleSummary())
	// The page's script splits the attribute on newlines. HTML keeps a newline
	// in an attribute value as it is, so the template must not flatten it.
	if !strings.Contains(out, "data-tip=\"Sun, Oct 4 2026\n1 session · 4 prompts · 4 calls\n") {
		t.Errorf("tooltip lines are not kept: %.200s", out[strings.Index(out, "data-tip"):])
	}
}

func TestRenderEmpty(t *testing.T) {
	out := render(t, session.Summary{GeneratedAt: time.Now()})
	if !strings.Contains(out, "No sessions yet") || strings.Contains(out, "<svg") || strings.Contains(out, "Tokens per day") {
		t.Errorf("empty page:\n%s", out)
	}

	filtered := render(t, session.Summary{GeneratedAt: time.Now(), CWD: "/x", Since: time.Now().AddDate(0, 0, -7)})
	if !strings.Contains(filtered, "No sessions match this period and project") || !strings.Contains(filtered, "Since ") || !strings.Contains(filtered, "· /x ·") {
		t.Errorf("filtered empty page:\n%s", filtered)
	}
}

func TestRenderExplainsMissingPrices(t *testing.T) {
	s := sampleSummary()
	s.Overview.Cost, s.Overview.SubscriptionCost = 0, 0
	for i := range s.Days {
		s.Days[i].Cost = 0
	}
	out := render(t, s)
	if strings.Count(out, "<svg") != 2 {
		t.Errorf("without prices there is no cost chart: %d svgs", strings.Count(out, "<svg"))
	}
	if !strings.Contains(out, "Nothing has a price yet") || !strings.Contains(out, "no prices set") {
		t.Error("the page must say why there is no cost")
	}
}

func TestRenderNotesSkippedLinesAndUsesTheZone(t *testing.T) {
	s := sampleSummary()
	s.Skipped = 1234
	var b bytes.Buffer
	if err := Render(&b, s, Options{Loc: time.FixedZone("UTC+7", 7*3600)}); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "1,234 unreadable lines") {
		t.Error("skipped lines are not mentioned")
	}
	if !strings.Contains(out, "Oct 6 16:00") || !strings.Contains(out, "Oct 8, 2026 21:02") {
		t.Error("times must be shown in the requested zone")
	}
}
