package route

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/phucvinh57/pi-go/internal/ai"
)

// fixed is an Assessor that always answers a, or fails with err.
type fixed struct {
	a     Assessment
	err   error
	calls int
}

func (f *fixed) Assess(context.Context, Request) (Assessment, error) {
	f.calls++
	return f.a, f.err
}

// three is a ladder of a local, a priced and a subscription model.
var three = []Candidate{
	{Ref: "ollama/small", Local: true, ContextWindow: 8000},
	{Ref: "openai/mid", Cost: ai.Rates{Input: 1, Output: 4}},
	{Ref: "openai-codex/big", Subscription: true, ContextWindow: 272_000},
}

func auto(prompt string) Request {
	return Request{Prompt: prompt, Candidates: three, AutoModel: true, AutoEffort: true}
}

func TestDecisionTable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		a       Assessment
		current string
		plan    bool
		ref     string
		effort  string
		note    string
	}{
		{"trivial goes to the weakest", Assessment{Kind: "chat", Demand: 0, Confidence: 0.9}, "", false, "ollama/small", "low", "chat, trivial"},
		{"ordinary goes to the middle", Assessment{Kind: "implement", Demand: 1.4, Confidence: 0.9}, "", false, "openai/mid", "medium", "implement, ordinary"},
		{"architectural goes to the strongest", Assessment{Kind: "refactor", Demand: 3.4, Confidence: 0.9}, "", false, "openai-codex/big", "xhigh", "refactor, architectural"},
		{"demanding asks for high effort", Assessment{Kind: "debug", Demand: 2, Confidence: 0.9}, "", false, "openai/mid", "high", "debug, demanding"},
		{"plans are never routed to the weakest", Assessment{Kind: "plan", Demand: 0.2, Confidence: 0.9}, "", false, "openai/mid", "high", "plan, demanding"},
		{"plan mode floors the demand too", Assessment{Kind: "explain", Demand: 0, Confidence: 0.9}, "", true, "openai/mid", "high", "explain, demanding"},
		{"close enough keeps the current model", Assessment{Kind: "implement", Demand: 2.1, Confidence: 0.9}, "openai/mid", false, "openai/mid", "high", "implement, demanding"},
		{"far away switches", Assessment{Kind: "implement", Demand: 3, Confidence: 0.9}, "ollama/small", false, "openai-codex/big", "xhigh", "implement, architectural"},
		{"unsure keeps the current model", Assessment{Kind: "implement", Demand: 3, Confidence: 0.06}, "ollama/small", false, "ollama/small", "xhigh", "implement, architectural"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := auto("please do the thing described here")
			req.Current, req.PlanMode = tc.current, tc.plan
			d, err := Router{Primary: &fixed{a: tc.a}}.Decide(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if d.Ref != tc.ref || d.Effort != tc.effort || d.Fallback {
				t.Errorf("got %s at %s (fallback %v), want %s at %s", d.Ref, d.Effort, d.Fallback, tc.ref, tc.effort)
			}
			if d.Note != tc.note {
				t.Errorf("note = %q, want %q", d.Note, tc.note)
			}
		})
	}
}

func TestDecideOnlyWhatIsAuto(t *testing.T) {
	a := &fixed{a: Assessment{Kind: "refactor", Demand: 3, Confidence: 1}}

	req := auto("rewrite the whole storage layer")
	req.AutoModel, req.Current, req.CurrentEffort = false, "ollama/pinned", ""
	d, err := Router{Primary: a}.Decide(context.Background(), req)
	if err != nil || d.Ref != "ollama/pinned" || d.Effort != "xhigh" {
		t.Errorf("auto effort only: %+v, %v", d, err)
	}

	req = auto("rewrite the whole storage layer")
	req.AutoEffort, req.CurrentEffort = false, "low"
	d, err = Router{Primary: a}.Decide(context.Background(), req)
	if err != nil || d.Ref != "openai-codex/big" || d.Effort != "low" {
		t.Errorf("auto model only: %+v, %v", d, err)
	}
}

func TestShortFollowUpStays(t *testing.T) {
	a := &fixed{a: Assessment{Demand: 3, Confidence: 1}}
	req := auto("ok, go on")
	req.Current, req.CurrentEffort = "ollama/small", "low"
	d, err := Router{Primary: a}.Decide(context.Background(), req)
	if err != nil || d.Ref != "ollama/small" || d.Effort != "low" || d.Note != "" || a.calls != 0 {
		t.Errorf("got %+v, %v after %d classifier calls", d, err, a.calls)
	}

	// Nothing to stay on yet: the first prompt is routed however short.
	d, err = Router{Primary: a}.Decide(context.Background(), auto("hi"))
	if err != nil || d.Ref != "openai-codex/big" || a.calls != 1 {
		t.Errorf("first prompt: %+v, %v", d, err)
	}
}

func TestClassifierFailureFallsBack(t *testing.T) {
	a := &fixed{err: errors.New("laya/english did not answer within 3.5s")}
	d, err := Router{Primary: a}.Decide(context.Background(), auto("rename the variable x to count"))
	if err != nil {
		t.Fatal(err)
	}
	if !d.Fallback || !strings.Contains(d.Note, "heuristic") || !strings.Contains(d.Note, "did not answer") {
		t.Errorf("decision = %+v, want a fallback that says why", d)
	}
	if d.Ref != "ollama/small" || d.Effort != "low" {
		t.Errorf("a rename went to %s at %s", d.Ref, d.Effort)
	}

	d, err = Router{}.Decide(context.Background(), auto("rename the variable x to count"))
	if err != nil || !d.Fallback || strings.Contains(d.Note, "(") {
		t.Errorf("no classifier: %+v, %v; want the heuristics without an error", d, err)
	}
}

func TestCancelledDecisionFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := &fixed{err: context.Canceled}
	if _, err := (Router{Primary: a}).Decide(ctx, auto("do the thing described here")); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want the cancellation, not a fallback", err)
	}
}

func TestNoCandidates(t *testing.T) {
	req := auto("do the thing described here")
	req.Candidates = nil
	if _, err := (Router{}).Decide(context.Background(), req); !errors.Is(err, ErrNoCandidates) {
		t.Errorf("err = %v", err)
	}
	req = auto("do the thing described here")
	req.Candidates = []Candidate{three[0], three[2]} // both with a known window
	req.ContextTokens = 1_000_000
	if _, err := (Router{}).Decide(context.Background(), req); err == nil || !strings.Contains(err.Error(), "room") {
		t.Errorf("err = %v, want no room", err)
	}
}

func TestContextTooBigSkipsSmallModels(t *testing.T) {
	req := auto("rename the variable x to count")
	req.ContextTokens = 10_000 // more than ollama/small's 8000
	d, err := Router{Primary: &fixed{a: Assessment{Kind: "chat", Demand: 0, Confidence: 1}}}.Decide(context.Background(), req)
	if err != nil || d.Ref != "openai/mid" {
		t.Errorf("got %+v, %v; want the weakest model with room", d, err)
	}
}

func TestLadderGuessesStrength(t *testing.T) {
	got := Ladder([]Candidate{
		{Ref: "openai-codex/gpt-6-sol", Subscription: true},
		{Ref: "openai/gpt-dear", Cost: ai.Rates{Input: 5, Output: 20}},
		{Ref: "ollama/qwen", Local: true},
		{Ref: "openai/text-embedding-3", Cost: ai.Rates{Input: 0.1}},
		{Ref: "openai/gpt-cheap", Cost: ai.Rates{Input: 0.1, Output: 0.4}},
		{Ref: "custom/unpriced"},
	}, nil)
	var refs []string
	for _, c := range got {
		refs = append(refs, c.Ref)
	}
	want := "ollama/qwen,custom/unpriced,openai/gpt-cheap,openai/gpt-dear,openai-codex/gpt-6-sol"
	if strings.Join(refs, ",") != want {
		t.Errorf("ladder = %v\nwant     %s", refs, want)
	}
}

func TestLadderBreaksTiesBySizeAndVersion(t *testing.T) {
	got := Ladder([]Candidate{
		{Ref: "ollama/deepseek-r1:70b", Local: true},
		{Ref: "ollama/llama3.2:1b", Local: true},
		{Ref: "ollama/mixtral:8x7b", Local: true},
		{Ref: "ollama/gemma3:270m", Local: true},
		{Ref: "ollama/qwen2.5-coder:7b", Local: true},
		{Ref: "openai-codex/gpt-6-sol", Subscription: true},
		{Ref: "openai-codex/gpt-5.10", Subscription: true},
		{Ref: "openai-codex/gpt-5.6-terra", Subscription: true},
		{Ref: "openai-codex/gpt-5.5", Subscription: true},
	}, nil)
	var refs []string
	for _, c := range got {
		refs = append(refs, c.Ref)
	}
	want := "ollama/gemma3:270m,ollama/llama3.2:1b,ollama/qwen2.5-coder:7b,ollama/mixtral:8x7b,ollama/deepseek-r1:70b," +
		"openai-codex/gpt-5.5,openai-codex/gpt-5.6-terra,openai-codex/gpt-5.10,openai-codex/gpt-6-sol"
	if strings.Join(refs, ",") != want {
		t.Errorf("ladder = %v\nwant     %s", refs, want)
	}
}

func TestLadderFollowsConfiguredOrder(t *testing.T) {
	got := Ladder(three, []string{"openai-codex/big", "missing/model", "ollama/small", "openai-codex/big"})
	if len(got) != 2 || got[0].Ref != "openai-codex/big" || got[1].Ref != "ollama/small" {
		t.Errorf("ladder = %+v", got)
	}
}

func TestHeuristic(t *testing.T) {
	bigFile := "<file path=\"a.go\">\n" + strings.Repeat("line\n", 500) + "end\n</file>"
	for _, tc := range []struct {
		prompt string
		plan   bool
		kind   string
		low    float64 // demand must be at least this
		high   float64 // and below this
	}{
		{"thanks!", false, "chat", -1, 0.5},
		{"rename the variable x to count in main.go", false, "refactor", -1, 0.75},
		{"add a --verbose flag to the build command that prints each step", false, "implement", 0.75, 1.5},
		{"find the root cause of the deadlock in the worker pool and fix it", false, "debug", 1.5, 3},
		{"what does this do?\n\n" + bigFile, false, "explain", 0.75, 1.5},
		{"look at the session package and tell me how saving works", true, "plan", 1.25, 2},
	} {
		a, err := Heuristic{}.Assess(context.Background(), Request{Prompt: tc.prompt, PlanMode: tc.plan})
		if err != nil {
			t.Fatal(err)
		}
		if a.Kind != tc.kind || a.Demand < tc.low || a.Demand >= tc.high {
			t.Errorf("%.40q: kind %s demand %.2f, want %s in [%.2f, %.2f)", tc.prompt, a.Kind, a.Demand, tc.kind, tc.low, tc.high)
		}
	}
}

func TestStripFiles(t *testing.T) {
	prompt := "explain\n\n<file path=\"dir/a \\\"q\\\".go\">\npackage a\n\nfunc A() {}\n</file>\n\n<file path=\"b.go\">\nx\n</file>"
	got := stripFiles(prompt)
	want := "explain\n\n[file dir/a \\\"q\\\".go attached, 3 lines]\n\n[file b.go attached, 1 lines]"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if n := countFiles(prompt); n != 2 {
		t.Errorf("countFiles = %d", n)
	}
}

// fakeClassifier answers with answers, after delay, and keeps what it was sent.
type fakeClassifier struct {
	answers map[string]ai.Answer
	usage   ai.Usage
	delay   time.Duration
	state   map[string]any
}

func (f *fakeClassifier) Classify(ctx context.Context, _ ai.Model, state map[string]any, qs map[string]ai.Question, _ ai.Options) (ai.Classification, error) {
	f.state = state
	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
		return ai.Classification{}, ctx.Err()
	}
	for id := range qs {
		if _, ok := f.answers[id]; !ok {
			return ai.Classification{}, errors.New("no answer for " + id)
		}
	}
	return ai.Classification{Answers: f.answers, Usage: f.usage}, nil
}

func TestLayaAssess(t *testing.T) {
	fc := &fakeClassifier{
		answers: map[string]ai.Answer{
			"task_kind":  {Type: ai.QuestionChoice, Choice: "debug", Confidence: 0.9},
			"complexity": {Type: ai.QuestionScore, Score: 1.8, Confidence: 0.7},
		},
		usage: ai.Usage{Input: 300, Output: 20},
	}
	j := Laya{Classifier: fc, Model: ai.Model{Provider: "laya", ID: "english"}}
	a, err := j.Assess(context.Background(), Request{
		Prompt:        "why does this fail?\n\n<file path=\"a.go\">\nsecret code\n</file>",
		LastReply:     "I changed a.go",
		PlanMode:      true,
		ContextTokens: 2000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.Kind != "debug" || a.Demand != 1.8 || a.Confidence != 0.7 || a.Usage.Input != 300 || a.UsageRef != "laya/english" {
		t.Errorf("assessment = %+v", a)
	}
	if p, _ := fc.state["prompt"].(string); strings.Contains(p, "secret code") || !strings.Contains(p, "[file a.go attached") {
		t.Errorf("prompt sent = %q, want file contents left out", p)
	}
	// Only the prompt and the previous reply: anything else made Laya's answers worse.
	if fc.state["previous_reply"] != "I changed a.go" || len(fc.state) != 2 {
		t.Errorf("state = %v", fc.state)
	}

	slow := &fakeClassifier{answers: fc.answers, delay: time.Second}
	j = Laya{Classifier: slow, Model: j.Model, Timeout: 10 * time.Millisecond}
	if _, err := j.Assess(context.Background(), Request{Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "did not answer within") {
		t.Errorf("err = %v, want a timeout", err)
	}
}

func TestLayaUsageReachesDecision(t *testing.T) {
	fc := &fakeClassifier{
		answers: map[string]ai.Answer{
			"task_kind":  {Choice: "implement"},
			"complexity": {Score: 1, Confidence: 0.9},
		},
		usage: ai.Usage{Input: 10, Cost: ai.Cost{Total: 0.01}},
	}
	r := Router{Primary: Laya{Classifier: fc, Model: ai.Model{Provider: "laya", ID: "english"}}}
	d, err := r.Decide(context.Background(), auto("add a flag that prints the version"))
	if err != nil || d.UsageRef != "laya/english" || d.Usage.Cost.Total != 0.01 || d.Fallback {
		t.Errorf("decision = %+v, %v", d, err)
	}
}
