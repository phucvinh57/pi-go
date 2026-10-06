package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestFormatTokens(t *testing.T) {
	for n, want := range map[int]string{
		0: "0", 999: "999", 1000: "1.0k", 1234: "1.2k", 9999: "10.0k",
		10_000: "10k", 45_600: "46k", 999_499: "999k",
		1_000_000: "1.0M", 1_500_000: "1.5M", 10_000_000: "10M", 12_600_000: "13M",
	} {
		if got := formatTokens(n); got != want {
			t.Errorf("formatTokens(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestGroupDigits(t *testing.T) {
	for n, want := range map[int]string{0: "0", 12: "12", 123: "123", 1234: "1,234", 1234567: "1,234,567", 100000: "100,000"} {
		if got := groupDigits(n); got != want {
			t.Errorf("groupDigits(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestUsagePartsLeaveOutZeros(t *testing.T) {
	if parts := (&Stats{CacheHit: -1}).usageParts(); len(parts) != 0 {
		t.Errorf("a fresh session shows %v", parts)
	}

	s := &Stats{Input: 1200, Output: 340, CacheRead: 5000, CacheWrite: 100, Cost: 0.0421, CacheHit: 80.5}
	got := strings.Join(s.usageParts(), " | ")
	if want := "↑1.2k ↓340 R5.0k W100 | CH80.5% | $0.042"; got != want {
		t.Errorf("parts = %q, want %q", got, want)
	}

	// No cache traffic: no hit rate, no R or W. No cost on a local model: no $.
	local := &Stats{Input: 10, Output: 5, CacheHit: 0}
	if got := strings.Join(local.usageParts(), " | "); got != "↑10 ↓5" {
		t.Errorf("local parts = %q", got)
	}
}

func TestSubscriptionShowsCostEvenWhenZero(t *testing.T) {
	s := &Stats{Input: 10, Subscription: true, CacheHit: -1}
	if got := strings.Join(s.usageParts(), " | "); got != "↑10 | $0.000 (sub)" {
		t.Errorf("parts = %q", got)
	}
	s.Cost = 1.5
	if got := s.usageParts(); got[len(got)-1] != "$1.500 (sub)" {
		t.Errorf("parts = %q", got)
	}
}

func TestContextPart(t *testing.T) {
	for _, c := range []struct {
		s    Stats
		want string
	}{
		{Stats{ContextWindow: 272_000, ContextTokens: 112_000, ContextPercent: 41.17}, "ctx 41.2%/272k"},
		{Stats{ContextWindow: 128_000, ContextPercent: 0}, "ctx 0.0%/128k"},
		{Stats{ContextTokens: 12_345}, "ctx 12k"},
		{Stats{}, ""},
	} {
		if got := c.s.contextPart(); got != c.want {
			t.Errorf("%+v: %q, want %q", c.s, got, c.want)
		}
	}
}

func footerOf(m *app) string {
	_, _, _, footer := m.bottom()
	return ansi.Strip(footer)
}

func TestFooterShowsStatsLineOnlyWithUsage(t *testing.T) {
	m, _ := newTestApp(nil)
	resize(m, 100, 20)
	if rows := strings.Split(footerOf(m), "\n"); len(rows) != 1 {
		t.Fatalf("a fresh session's footer has %d rows, want only the hints row", len(rows))
	}

	m.Update(eventMsg{Event{Kind: EventStats, Stats: &Stats{
		Input: 1200, Output: 340, Cost: 0.5, CacheHit: -1,
		ContextTokens: 112_000, ContextWindow: 272_000, ContextPercent: 41.2,
	}}})
	rows := strings.Split(footerOf(m), "\n")
	if len(rows) != 2 {
		t.Fatalf("footer = %q", rows)
	}
	if want := " ↑1.2k ↓340 · $0.500 · ctx 41.2%/272k"; rows[0] != want {
		t.Errorf("stats row = %q, want %q", rows[0], want)
	}
	if !strings.Contains(rows[1], "/ for commands") {
		t.Errorf("hints row = %q", rows[1])
	}
}

func TestFooterKeepsContextOnNarrowTerminals(t *testing.T) {
	m, _ := newTestApp(nil)
	m.stats = &Stats{
		Input: 123_456, Output: 78_900, CacheRead: 4_000_000, Cost: 12.3456, CacheHit: 91,
		ContextTokens: 250_000, ContextWindow: 272_000, ContextPercent: 91.9,
	}
	for _, width := range []int{30, 40, 60} {
		resize(m, width, 20)
		row := strings.Split(footerOf(m), "\n")[0]
		if w := ansi.StringWidth(row); w > width {
			t.Errorf("width %d: stats row is %d cells: %q", width, w, row)
		}
		if !strings.HasSuffix(row, "ctx 91.9%/272k") {
			t.Errorf("width %d: the context figure was cut: %q", width, row)
		}
	}
}

func TestFooterColoursContextByFullness(t *testing.T) {
	m, _ := newTestApp(nil)
	resize(m, 80, 20)
	// The row with the percentage text blanked out, so what is left to compare
	// is the styling alone.
	styling := func(percent float64) string {
		m.stats = &Stats{ContextWindow: 1000, ContextPercent: percent}
		_, _, _, footer := m.bottom()
		row := strings.Split(footer, "\n")[0]
		return strings.Replace(row, fmt.Sprintf("%.1f%%", percent), "P%", 1)
	}
	plain, warn, bad := styling(50), styling(80), styling(95)
	if plain == warn || warn == bad || plain == bad {
		t.Errorf("up to 70%%, 70-90%% and over 90%% must look different:\n%q\n%q\n%q", plain, warn, bad)
	}
	if styling(70) != plain {
		t.Error("exactly 70% is not a warning yet")
	}
	if styling(90) != warn {
		t.Error("exactly 90% is a warning, not an error yet")
	}
	if styling(70.1) != warn || styling(90.1) != bad {
		t.Error("just past a threshold must change the colour")
	}
}

func TestSessionCommand(t *testing.T) {
	m, _ := newTestApp(nil)
	resize(m, 100, 40)

	run := func() string {
		typeText(m, "/session")
		press(m, 13) // enter
		return ansi.Strip(m.tr.render(100, true))
	}
	if got := run(); !strings.Contains(got, "No usage yet") {
		t.Errorf("before any call:\n%s", got)
	}

	m.Update(eventMsg{Event{Kind: EventStats, Stats: &Stats{
		UserMessages: 2, AssistantMessages: 3, ToolCalls: 1, ToolResults: 1,
		Input: 1000, Output: 250, CacheRead: 3000, CacheWrite: 500, Cost: 0.1234,
		ByModel:  []ModelCost{{Ref: "a/one", Cost: 0.1}, {Ref: "b/two", Cost: 0.0234}},
		CacheHit: 66.7, ContextTokens: 54_321, ContextWindow: 272_000, ContextPercent: 19.97,
	}}})
	got := run()
	for _, want := range []string{
		"user 2 · assistant 3 · tool calls 1 · tool results 1",
		"input    4,500",
		"cached   3,000 (66.7% of input)",
		"uncached 1,500 (500 written to the cache)",
		"output   250",
		"total    4,750",
		"Cost  $0.1234",
		"a/one", "b/two",
		"Context  20.0% of 272,000 (54,321 tokens)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("/session is missing %q:\n%s", want, got)
		}
	}
}

func TestSessionReportForSubscriptionAndUnknownWindow(t *testing.T) {
	got := (&Stats{Input: 10, Cost: 2, Subscription: true, ContextTokens: 99}).report()
	if !strings.Contains(got, "subscription") || !strings.Contains(got, "99 tokens (window unknown") {
		t.Errorf("report:\n%s", got)
	}
	if strings.Contains(got, "$2.0000 \n") || strings.Count(got, "a/") > 0 {
		t.Errorf("report:\n%s", got)
	}
	// A single model needs no breakdown.
	one := (&Stats{ByModel: []ModelCost{{Ref: "solo/m", Cost: 1}}}).report()
	if strings.Contains(one, "solo/m") {
		t.Errorf("a one-model session needs no breakdown:\n%s", one)
	}
}
