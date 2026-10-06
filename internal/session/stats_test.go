package session

import (
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/phucvinh57/pi-go/internal/ai"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// builder assembles a Session in memory, with timestamps under the test's
// control.
type builder struct{ s *Session }

func newSess(id, cwd, start string) *builder {
	return &builder{&Session{
		Path:   "/sessions/" + id + ".jsonl",
		Header: Header{Type: TypeSession, Version: Version, ID: id, Timestamp: formatTime(at(start)), CWD: cwd},
	}}
}

func (b *builder) entry(ts string, e Entry) *builder {
	e.Type = orDefault(e.Type, TypeMessage)
	e.ID = "e"
	e.Timestamp = formatTime(at(ts))
	b.s.Entries = append(b.s.Entries, e)
	return b
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func (b *builder) model(ts, provider, id string) *builder {
	return b.entry(ts, Entry{Type: TypeModelChange, Provider: provider, ModelID: id})
}

func (b *builder) user(ts, text string) *builder {
	m := ai.UserText(text)
	return b.entry(ts, Entry{Message: &m})
}

func (b *builder) call(ts, provider, model string, in, out, cacheRead, cacheWrite int, cost float64, stop ai.StopReason) *builder {
	m := ai.Message{
		Role: ai.RoleAssistant, Provider: provider, Model: model, StopReason: stop,
		Usage: ai.Usage{
			Input: in, Output: out, CacheRead: cacheRead, CacheWrite: cacheWrite,
			TotalTokens: in + out + cacheRead + cacheWrite, Cost: ai.Cost{Total: cost},
		},
	}
	return b.entry(ts, Entry{Message: &m})
}

func (b *builder) tool(ts, name string, isError bool) *builder {
	m := ai.ToolResult("c", name, "out", isError)
	return b.entry(ts, Entry{Message: &m})
}

func (b *builder) done() *Session { return b.s }

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// fixture is three sessions in two projects over four days.
func fixture() []*Session {
	longPrompt := strings.Repeat("word ", 40) // 200 chars

	a := newSess("a", "/p/a", "2026-10-04T10:00:00Z").
		model("2026-10-04T10:00:00Z", "ollama", "qwen").
		user("2026-10-04T10:00:01Z", "  fix the\n bug  in parser ").
		call("2026-10-04T10:00:02Z", "ollama", "qwen", 100, 10, 0, 0, 0.5, ai.StopToolUse).
		tool("2026-10-04T10:00:03Z", "read", false).
		tool("2026-10-04T10:00:04Z", "bash", true).
		call("2026-10-04T10:00:05Z", "ollama", "qwen", 50, 20, 100, 10, 0.25, ai.StopStop).done()

	b := newSess("b", "/p/b", "2026-10-06T09:00:00Z").
		model("2026-10-06T09:00:00Z", "openai-codex", "gpt-5.5").
		user("2026-10-06T09:00:01Z", longPrompt).
		call("2026-10-06T09:00:02Z", "openai-codex", "gpt-5.5", 200, 40, 0, 0, 2, ai.StopStop).
		call("2026-10-06T09:05:00Z", "openai-codex", "gpt-5.5", 30, 0, 0, 0, 0.1, ai.StopError).done()
	b.Skipped = 2

	// Crosses midnight UTC.
	c := newSess("c", "/p/a", "2026-10-06T23:30:00Z").
		user("2026-10-06T23:30:01Z", "late one").
		call("2026-10-06T23:31:00Z", "ollama", "qwen", 10, 1, 0, 0, 0, ai.StopStop).
		call("2026-10-07T00:10:00Z", "ollama", "qwen", 20, 2, 0, 0, 0, ai.StopStop).done()

	return []*Session{a, b, c}
}

func summarize(f Filter) Summary {
	if f.Loc == nil {
		f.Loc = time.UTC
	}
	if f.Subscription == nil {
		f.Subscription = func(p string) bool { return p == "openai-codex" }
	}
	f.Now = at("2026-10-08T12:00:00Z")
	return Summarize(fixture(), f)
}

func TestSummarizeOverview(t *testing.T) {
	ov := summarize(Filter{}).Overview
	if ov.Sessions != 3 || ov.Prompts != 3 || ov.Calls != 6 || ov.FailedCalls != 1 || ov.ToolCalls != 2 {
		t.Errorf("counts = %+v", ov)
	}
	if want := (Tokens{Input: 410, Output: 73, CacheRead: 100, CacheWrite: 10}); ov.Tokens != want || ov.TotalTokens != 593 {
		t.Errorf("tokens = %+v total %d", ov.Tokens, ov.TotalTokens)
	}
	// The failed call was billed, so it is in the totals.
	if !near(ov.Cost, 2.85) || !near(ov.SubscriptionCost, 2.1) {
		t.Errorf("cost = %v, subscription part %v; want 2.85 and 2.1", ov.Cost, ov.SubscriptionCost)
	}
	if want := 100.0 / 520.0 * 100; !near(ov.CacheHit, want) {
		t.Errorf("cache hit = %v, want %v", ov.CacheHit, want)
	}
	if !ov.First.Equal(at("2026-10-04T10:00:01Z")) || !ov.Last.Equal(at("2026-10-07T00:10:00Z")) {
		t.Errorf("span = %v .. %v", ov.First, ov.Last)
	}
	if got := summarize(Filter{}).Skipped; got != 2 {
		t.Errorf("skipped lines = %d, want 2", got)
	}
}

func TestSummarizeDaysIncludeQuietOnes(t *testing.T) {
	days := summarize(Filter{}).Days
	var dates []string
	for _, d := range days {
		dates = append(dates, d.Date)
	}
	if got := strings.Join(dates, " "); got != "2026-10-04 2026-10-05 2026-10-06 2026-10-07" {
		t.Fatalf("days = %s", got)
	}
	if q := days[1]; q.Calls != 0 || q.Sessions != 0 || q.Tokens.Total() != 0 {
		t.Errorf("the quiet day = %+v", q)
	}
	d6 := days[2]
	if d6.Sessions != 2 || d6.Prompts != 2 || d6.Calls != 3 || !near(d6.Cost, 2.1) {
		t.Errorf("10-06 = %+v", d6)
	}
	// The session that crossed midnight counts on both days it was active.
	if d7 := days[3]; d7.Sessions != 1 || d7.Calls != 1 || d7.Prompts != 0 {
		t.Errorf("10-07 = %+v", d7)
	}
}

func TestSummarizeUsesTheFilterTimeZone(t *testing.T) {
	// At UTC+2 the late message of session c (23:31Z) is already on the 7th.
	plus2 := time.FixedZone("UTC+2", 2*3600)
	days := summarize(Filter{Loc: plus2}).Days
	last := days[len(days)-1]
	if last.Date != "2026-10-07" || last.Calls != 2 {
		t.Errorf("last day at UTC+2 = %+v, want both late calls on 10-07", last)
	}
}

func TestSummarizeModels(t *testing.T) {
	models := summarize(Filter{}).Models
	if len(models) != 2 {
		t.Fatalf("models = %+v", models)
	}
	codex, qwen := models[0], models[1] // by cost: 2.1 then 0.75
	if codex.Ref != "openai-codex/gpt-5.5" || !codex.Subscription || codex.Calls != 2 || codex.FailedCalls != 1 || codex.TotalTokens != 270 {
		t.Errorf("codex = %+v", codex)
	}
	if qwen.Ref != "ollama/qwen" || qwen.Subscription || qwen.Calls != 4 || qwen.TotalTokens != 323 || !near(qwen.Cost, 0.75) {
		t.Errorf("qwen = %+v", qwen)
	}
	if !near(codex.Share+qwen.Share, 100) || !near(codex.Share, 270.0/593*100) {
		t.Errorf("shares = %v + %v", codex.Share, qwen.Share)
	}
}

func TestSummarizeProjectsAndTools(t *testing.T) {
	s := summarize(Filter{})
	if len(s.Projects) != 2 || s.Projects[0].CWD != "/p/b" || s.Projects[1].CWD != "/p/a" {
		t.Fatalf("projects = %+v", s.Projects)
	}
	if a := s.Projects[1]; a.Sessions != 2 || a.Calls != 4 || a.TotalTokens != 323 || !near(a.Cost, 0.75) {
		t.Errorf("/p/a = %+v", a)
	}

	want := []ToolStat{{"bash", 1, 1}, {"read", 1, 0}} // equal calls: by name
	if len(s.Tools) != 2 || s.Tools[0] != want[0] || s.Tools[1] != want[1] {
		t.Errorf("tools = %+v, want %+v", s.Tools, want)
	}
}

func TestSummarizeRecentSessions(t *testing.T) {
	r := summarize(Filter{}).Recent
	if len(r) != 3 || r[0].ID != "c" || r[1].ID != "b" || r[2].ID != "a" {
		t.Fatalf("recent = %+v", r)
	}
	a := r[2]
	if a.FirstPrompt != "fix the bug in parser" {
		t.Errorf("whitespace must collapse: %q", a.FirstPrompt)
	}
	if a.Prompts != 1 || a.Calls != 2 || a.TotalTokens != 290 || !near(a.Cost, 0.75) || a.Path != "/sessions/a.jsonl" || a.CWD != "/p/a" {
		t.Errorf("a = %+v", a)
	}
	if len(a.Models) != 1 || a.Models[0] != "ollama/qwen" {
		t.Errorf("a models = %v (a model_change and the calls name the same model)", a.Models)
	}

	long := r[1].FirstPrompt
	if n := utf8.RuneCountInString(long); n != 80 || !strings.HasSuffix(long, "…") {
		t.Errorf("long prompt = %d runes %q, want 80 ending in an ellipsis", n, long)
	}
}

func TestSummarizeSinceCountsOnlyWhatIsInRange(t *testing.T) {
	s := summarize(Filter{Since: at("2026-10-06T12:00:00Z")})
	// Session a and b are before it; c is entirely after it.
	if s.Overview.Sessions != 1 || s.Overview.Calls != 2 || s.Overview.Prompts != 1 {
		t.Errorf("overview = %+v", s.Overview)
	}
	if len(s.Days) != 2 || s.Days[0].Date != "2026-10-06" {
		t.Errorf("days = %+v", s.Days)
	}
	if len(s.Models) != 1 || s.Models[0].Ref != "ollama/qwen" {
		t.Errorf("models = %+v", s.Models)
	}

	// A session that straddles the cut counts only for the part after it.
	s = summarize(Filter{Since: at("2026-10-07T00:00:00Z")})
	if s.Overview.Sessions != 1 || s.Overview.Calls != 1 || s.Overview.Prompts != 0 || s.Overview.Tokens.Input != 20 {
		t.Errorf("straddling session: %+v", s.Overview)
	}
}

func TestSummarizeCWDFilter(t *testing.T) {
	s := summarize(Filter{CWD: "/p/a"})
	if s.Overview.Sessions != 2 || s.Overview.Calls != 4 || len(s.Projects) != 1 || s.Projects[0].CWD != "/p/a" {
		t.Errorf("summary = %+v / %+v", s.Overview, s.Projects)
	}
	if s.Overview.SubscriptionCost != 0 || len(s.Models) != 1 {
		t.Errorf("project b leaked in: %+v", s.Models)
	}
	if s.Skipped != 0 {
		t.Errorf("skipped lines of a filtered-out session were counted: %d", s.Skipped)
	}
}

func TestSummarizeEmpty(t *testing.T) {
	s := Summarize(nil, Filter{Loc: time.UTC})
	if s.Overview.Sessions != 0 || len(s.Days) != 0 || len(s.Models) != 0 || len(s.Recent) != 0 {
		t.Errorf("summary = %+v", s)
	}
	if s.Overview.CacheHit >= 0 {
		t.Errorf("no calls: cache hit must be negative, got %v", s.Overview.CacheHit)
	}
	if s.GeneratedAt.IsZero() {
		t.Error("GeneratedAt is not set")
	}
}

func TestSummarizeRecentIsLimited(t *testing.T) {
	var many []*Session
	for i := 0; i < 70; i++ {
		ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Hour).Format(time.RFC3339)
		many = append(many, newSess(string(rune('a'+i%26))+ts, "/p", ts).user(ts, "hi").done())
	}
	s := Summarize(many, Filter{Loc: time.UTC})
	if s.Overview.Sessions != 70 || len(s.Recent) != 50 {
		t.Errorf("sessions %d, recent %d; want 70 and 50", s.Overview.Sessions, len(s.Recent))
	}
	if s.Recent[0].Start.Before(s.Recent[49].Start) {
		t.Error("recent must be newest first")
	}
}

func TestSummarizeSkipsEmptySessionsAndUnknownModels(t *testing.T) {
	onlyModel := newSess("x", "/p", "2026-10-04T10:00:00Z").model("2026-10-04T10:00:00Z", "ollama", "qwen").done()
	noIdentity := newSess("y", "/p", "2026-10-04T11:00:00Z").
		call("2026-10-04T11:00:01Z", "", "", 5, 1, 0, 0, 0, ai.StopStop).done()
	s := Summarize([]*Session{onlyModel, noIdentity}, Filter{Loc: time.UTC})
	if s.Overview.Sessions != 1 {
		t.Errorf("a session with no messages must not count: %d", s.Overview.Sessions)
	}
	if len(s.Models) != 1 || s.Models[0].Ref != "unknown" {
		t.Errorf("models = %+v", s.Models)
	}
}

func TestSummarizeCountsCharges(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir, "/work/p", newClock().now)
	w.Charge("laya/english", ai.Usage{Input: 300, Output: 20, Cost: ai.Cost{Total: 0.5}})
	w.ModelChange("ollama", "qwen")
	w.Record(ai.UserText("split the package"))
	w.Record(assistant("done", 100, 10, 16))
	if err := w.Err(); err != nil {
		t.Fatal(err)
	}
	s, err := Load(w.Path())
	if err != nil {
		t.Fatal(err)
	}

	sum := Summarize([]*Session{s}, Filter{Loc: time.UTC})
	if ov := sum.Overview; !near(ov.Cost, 16.5) || ov.Tokens.Input != 400 || ov.Prompts != 1 {
		t.Errorf("overview = %+v, want the reply and the classifier", ov)
	}
	var laya *ModelStat
	for i := range sum.Models {
		if sum.Models[i].Ref == "laya/english" {
			laya = &sum.Models[i]
		}
	}
	if laya == nil || !near(laya.Cost, 0.5) || laya.Tokens.Input != 300 {
		t.Errorf("models = %+v", sum.Models)
	}
}
