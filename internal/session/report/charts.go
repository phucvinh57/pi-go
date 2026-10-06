package report

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/phucvinh57/pi-go/internal/session"
)

// Chart geometry. The charts are drawn as SVG in a fixed coordinate system
// (viewBox) and scale to the page width.
const (
	chartW = 760.0
	chartH = 260.0

	padLeft   = 52.0 // room for the y-axis labels
	padRight  = 14.0
	padTop    = 14.0
	padBottom = 28.0 // room for the x-axis labels

	maxBarW   = 24.0 // columns never fill their slot
	segGap    = 2.0  // surface-coloured gap between stacked segments
	endRadius = 4.0  // rounded data end of a bar
	maxDays   = 90   // the daily charts show the most recent days only
	maxXLabel = 8    // x-axis labels, at most
	barRows   = 10   // models in the bar chart before the rest fold into "Other"
)

type tick struct {
	Pos   float64
	Label string
}

// seg is one coloured segment of a stacked column.
type seg struct {
	Class string // s1, s2, s3: the series slot
	Path  string
}

type column struct {
	Segs []seg
	// The hover target is the whole column band, much wider than the mark.
	HitX, HitY, HitW, HitH float64
	Tip                    string
}

type legendItem struct{ Class, Name string }

// stackedChart is the tokens-per-day chart.
type stackedChart struct {
	W, H, X0, Y0, X1, Y1 float64
	YTicks               []tick
	XTicks               []tick
	Cols                 []column
	Legend               []legendItem
	Note                 string // set when older days are not drawn
	Label                string // for screen readers
}

type point struct{ X, Y float64 }

// lineChart is the cost-per-day chart.
type lineChart struct {
	W, H, X0, Y0, X1, Y1 float64
	YTicks               []tick
	XTicks               []tick
	Line, Area           string // SVG path data
	Dots                 []point
	End                  point // the last point, drawn as a dot with its value
	EndLabel             string
	EndAnchor            string
	Hits                 []column // only the hover fields are used
	Note                 string
	Label                string
}

type barRow struct {
	Label      string
	Tip        string
	Y          float64
	TextY      float64 // baseline of the label and the value
	Path       string
	ValueX     float64
	Value      string
	LabelX     float64
	RowH, BarH float64
	HitY, HitH float64
}

// barChart is the tokens-by-model chart.
type barChart struct {
	W, H  float64
	Rows  []barRow
	Label string
}

// niceScale picks a round axis for values up to max: about four steps of 1, 2,
// 2.5 or 5 times a power of ten. It returns the top of the axis and the step.
func niceScale(max float64) (top, step float64) {
	if max <= 0 {
		return 1, 1
	}
	raw := max / 4
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	for _, n := range []float64{1, 2, 2.5, 5, 10} {
		if raw <= n*mag {
			step = n * mag
			break
		}
	}
	return step * math.Ceil(max/step-1e-9), step
}

// formatTokens writes a token count compactly: 950, 1.2k, 45k, 1.5M.
func formatTokens(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprint(n)
	case n < 10_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	case n < 1_000_000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	case n < 10_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	return fmt.Sprintf("%dM", (n+500_000)/1_000_000)
}

// axisTokens labels a y-axis value; tick values are round, so trailing ".0"
// goes.
func axisTokens(v float64) string {
	return strings.Replace(formatTokens(int(math.Round(v))), ".0", "", 1)
}

// money writes dollars with as many decimals as keep small amounts visible.
func money(v float64) string {
	switch {
	case v == 0:
		return "$0"
	case v < 0.01:
		return fmt.Sprintf("$%.4f", v)
	case v < 1:
		return fmt.Sprintf("$%.3f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

func axisMoney(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("$%d", int(v))
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("$%.2f", v), "0"), ".")
}

// recentDays returns the days to draw and a note when some are left out.
func recentDays(days []session.Day) ([]session.Day, string) {
	if len(days) <= maxDays {
		return days, ""
	}
	return days[len(days)-maxDays:], fmt.Sprintf("Showing the last %d of %d days; the table has all of them.", maxDays, len(days))
}

// dayLabel is a short x-axis label for a 2006-01-02 date.
func dayLabel(date string) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return t.Format("Jan 2")
}

func dayTitle(date string) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return t.Format("Mon, Jan 2 2006")
}

// xTicks labels at most maxXLabel evenly spaced columns, the first included.
func xTicks(days []session.Day, x0, band float64) []tick {
	stride := (len(days) + maxXLabel - 1) / maxXLabel
	var ticks []tick
	for i := 0; i < len(days); i += max(stride, 1) {
		ticks = append(ticks, tick{Pos: x0 + (float64(i)+0.5)*band, Label: dayLabel(days[i].Date)})
	}
	return ticks
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// dayTip is the hover text of a day: lines are separated by newlines.
func dayTip(d session.Day) string {
	lines := []string{dayTitle(d.Date)}
	if d.Calls == 0 && d.Prompts == 0 {
		return strings.Join(append(lines, "No activity"), "\n")
	}
	lines = append(lines,
		fmt.Sprintf("%s · %s · %s", plural(d.Sessions, "session", "sessions"), plural(d.Prompts, "prompt", "prompts"), plural(d.Calls, "call", "calls")),
		fmt.Sprintf("Uncached input %s · Cached input %s · Output %s",
			formatTokens(d.Tokens.Input+d.Tokens.CacheWrite), formatTokens(d.Tokens.CacheRead), formatTokens(d.Tokens.Output)),
		money(d.Cost))
	return strings.Join(lines, "\n")
}

// roundedTop is the path of a column segment whose top corners are rounded and
// whose bottom is square, so it sits flat on what is below it.
func roundedTop(x, y, w, h, r float64) string {
	r = math.Min(r, math.Min(w/2, h))
	return fmt.Sprintf("M%.2f %.2f V%.2f Q%.2f %.2f %.2f %.2f H%.2f Q%.2f %.2f %.2f %.2f V%.2f Z",
		x, y+h, y+r, x, y, x+r, y, x+w-r, x+w, y, x+w, y+r, y+h)
}

func rect(x, y, w, h float64) string {
	return fmt.Sprintf("M%.2f %.2f H%.2f V%.2f H%.2f Z", x, y, x+w, y+h, x)
}

// tokenSeries are the stacked series, bottom to top. Cache writes count as
// uncached input, as PI's footer does.
var tokenSeries = []struct {
	Class, Name string
	Value       func(session.Tokens) int
}{
	{"s1", "Uncached input", func(t session.Tokens) int { return t.Input + t.CacheWrite }},
	{"s2", "Cached input", func(t session.Tokens) int { return t.CacheRead }},
	{"s3", "Output", func(t session.Tokens) int { return t.Output }},
}

func newStackedChart(all []session.Day) *stackedChart {
	days, note := recentDays(all)
	c := &stackedChart{
		W: chartW, H: chartH,
		X0: padLeft, Y0: padTop, X1: chartW - padRight, Y1: chartH - padBottom,
		Note: note,
	}
	for _, s := range tokenSeries {
		c.Legend = append(c.Legend, legendItem{s.Class, s.Name})
	}
	if len(days) == 0 {
		return c
	}

	maxTotal := 0
	for _, d := range days {
		maxTotal = max(maxTotal, d.Tokens.Total())
	}
	top, step := niceScale(float64(maxTotal))
	plotW, plotH := c.X1-c.X0, c.Y1-c.Y0
	scale := plotH / top
	band := plotW / float64(len(days))
	barW := math.Max(1, math.Min(maxBarW, band*0.7))

	for v := 0.0; v <= top+1e-9; v += step {
		c.YTicks = append(c.YTicks, tick{Pos: c.Y1 - v*scale, Label: axisTokens(v)})
	}
	c.XTicks = xTicks(days, c.X0, band)

	for i, d := range days {
		col := column{
			HitX: c.X0 + float64(i)*band, HitY: c.Y0, HitW: band, HitH: plotH,
			Tip: dayTip(d),
		}
		x := c.X0 + float64(i)*band + (band-barW)/2

		// The topmost segment that has any tokens gets the rounded end.
		topmost := -1
		for k, s := range tokenSeries {
			if s.Value(d.Tokens) > 0 {
				topmost = k
			}
		}
		cursor := c.Y1
		drawn := false
		for k, s := range tokenSeries {
			v := s.Value(d.Tokens)
			if v <= 0 {
				continue
			}
			h := float64(v) * scale
			y := cursor - h
			drawH := h
			if drawn {
				drawH -= segGap // the gap sits between this segment and the one below
			}
			cursor = y
			if drawH < 0.5 {
				continue
			}
			path := rect(x, y, barW, drawH)
			if k == topmost {
				path = roundedTop(x, y, barW, drawH, endRadius)
			}
			col.Segs = append(col.Segs, seg{Class: s.Class, Path: path})
			drawn = true
		}
		c.Cols = append(c.Cols, col)
	}
	c.Label = fmt.Sprintf("Stacked columns of tokens per day over %s; the table below has the numbers.", plural(len(days), "day", "days"))
	return c
}

// newLineChart draws cost per day. It returns nil when nothing cost anything:
// a flat line at zero says nothing.
func newLineChart(all []session.Day) *lineChart {
	days, note := recentDays(all)
	maxCost := 0.0
	for _, d := range days {
		maxCost = math.Max(maxCost, d.Cost)
	}
	if len(days) == 0 || maxCost <= 0 {
		return nil
	}

	c := &lineChart{
		W: chartW, H: chartH,
		X0: padLeft, Y0: padTop, X1: chartW - padRight, Y1: chartH - padBottom,
		Note: note,
	}
	top, step := niceScale(maxCost)
	plotW, plotH := c.X1-c.X0, c.Y1-c.Y0
	scale := plotH / top
	band := plotW / float64(len(days))

	for v := 0.0; v <= top+1e-9; v += step {
		c.YTicks = append(c.YTicks, tick{Pos: c.Y1 - v*scale, Label: axisMoney(v)})
	}
	c.XTicks = xTicks(days, c.X0, band)

	var line strings.Builder
	for i, d := range days {
		p := point{X: c.X0 + (float64(i)+0.5)*band, Y: c.Y1 - d.Cost*scale}
		c.Dots = append(c.Dots, p)
		cmd := "L"
		if i == 0 {
			cmd = "M"
		}
		fmt.Fprintf(&line, "%s%.2f %.2f ", cmd, p.X, p.Y)
		c.Hits = append(c.Hits, column{
			HitX: c.X0 + float64(i)*band, HitY: c.Y0, HitW: band, HitH: plotH, Tip: dayTip(d),
		})
	}
	c.Line = strings.TrimSpace(line.String())
	first, last := c.Dots[0], c.Dots[len(c.Dots)-1]
	c.Area = fmt.Sprintf("%s L%.2f %.2f L%.2f %.2f Z", c.Line, last.X, c.Y1, first.X, c.Y1)
	c.End = last
	c.EndLabel = money(days[len(days)-1].Cost)
	// Keep the end label inside the chart.
	c.EndAnchor = "middle"
	if last.X > c.X1-40 {
		c.EndAnchor = "end"
	}
	c.Label = fmt.Sprintf("Line of cost per day over %s; the table below has the numbers.", plural(len(days), "day", "days"))
	return c
}

// shorten cuts s to at most n runes, ending in an ellipsis when it was cut.
func shorten(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func newBarChart(models []session.ModelStat) *barChart {
	if len(models) == 0 {
		return nil
	}
	const (
		rowH   = 30.0
		barH   = 16.0
		labelW = 236.0 // right-aligned labels end here
		barX0  = 246.0
		valueW = 64.0
	)
	// Longest bar first, whatever order the table is in.
	sorted := append([]session.ModelStat(nil), models...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].TotalTokens != sorted[j].TotalTokens {
			return sorted[i].TotalTokens > sorted[j].TotalTokens
		}
		return sorted[i].Ref < sorted[j].Ref
	})
	rows := sorted
	var other session.ModelStat
	if len(sorted) > barRows {
		rows = sorted[:barRows]
		for _, m := range sorted[barRows:] {
			other.Calls += m.Calls
			other.TotalTokens += m.TotalTokens
			other.Cost += m.Cost
			other.Share += m.Share
		}
		other.Ref = fmt.Sprintf("Other (%d models)", len(models)-barRows)
	}

	maxTotal := 1
	for _, m := range rows {
		maxTotal = max(maxTotal, m.TotalTokens)
	}
	if other.Ref != "" {
		maxTotal = max(maxTotal, other.TotalTokens)
	}
	barMax := chartW - barX0 - valueW

	c := &barChart{W: chartW}
	add := func(m session.ModelStat) {
		y := float64(len(c.Rows)) * rowH
		w := float64(m.TotalTokens) / float64(maxTotal) * barMax
		if m.TotalTokens > 0 {
			w = math.Max(w, 2)
		}
		r := barRow{
			Label: shorten(m.Ref, 36), Y: y + (rowH-barH)/2, LabelX: labelW,
			Value: formatTokens(m.TotalTokens), ValueX: barX0 + w + 8,
			RowH: rowH, BarH: barH, HitY: y, HitH: rowH,
			Tip: fmt.Sprintf("%s\n%s · %s tokens (%.1f%%) · %s", m.Ref, plural(m.Calls, "call", "calls"), formatTokens(m.TotalTokens), m.Share, money(m.Cost)),
		}
		r.TextY = r.Y + barH/2 + 4 // 4px: half the cap height of the 12px labels
		if w > 0 {
			// Rounded at the data end (right), square at the baseline (left).
			r.Path = roundedRight(barX0, r.Y, w, barH, endRadius)
		}
		c.Rows = append(c.Rows, r)
	}
	for _, m := range rows {
		add(m)
	}
	if other.Ref != "" {
		add(other)
	}
	c.H = float64(len(c.Rows)) * rowH
	c.Label = fmt.Sprintf("Bars of tokens for each of %s; the table below has the numbers.", plural(len(models), "model", "models"))
	return c
}

func roundedRight(x, y, w, h, r float64) string {
	r = math.Min(r, math.Min(h/2, w))
	return fmt.Sprintf("M%.2f %.2f H%.2f Q%.2f %.2f %.2f %.2f V%.2f Q%.2f %.2f %.2f %.2f H%.2f Z",
		x, y, x+w-r, x+w, y, x+w, y+r, y+h-r, x+w, y+h, x+w-r, y+h, x)
}
