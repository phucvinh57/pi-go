package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/phucvinh57/pi-go/internal/ai"
)

// seen is a script that also records which model and effort each call used.
type seen struct {
	script
	models  []string
	efforts []string
}

func (s *seen) Stream(ctx context.Context, m ai.Model, c ai.Context, o ai.Options) <-chan ai.Event {
	s.models = append(s.models, m.Ref())
	s.efforts = append(s.efforts, o.Reasoning)
	return s.script.Stream(ctx, m, c, o)
}

// fakeRouter routes user requests to model "strong" at effort "high", charges
// a classifier call for each, and keeps continuations where they are.
type fakeRouter struct {
	p    ai.Provider
	reqs []RouteRequest
	err  error
}

func (f *fakeRouter) Route(_ context.Context, req RouteRequest) (Route, error) {
	f.reqs = append(f.reqs, req)
	if f.err != nil {
		return Route{Charges: []Charge{{Ref: "laya/english", Usage: ai.Usage{Input: 5, Cost: ai.Cost{Total: 0.5}}}}}, f.err
	}
	if req.Reason != RouteUser {
		return req.Current, nil
	}
	return Route{
		Provider: f.p,
		Model:    ai.Model{Provider: "ollama", ID: "strong", ContextWindow: 1000},
		Options:  ai.Options{Reasoning: "high"},
		Note:     "implement, demanding",
		Charges:  []Charge{{Ref: "laya/english", Usage: ai.Usage{Input: 300, Output: 20, Cost: ai.Cost{Total: 0.25}}}},
	}, nil
}

func TestRouterPicksModelOncePerPrompt(t *testing.T) {
	p := &seen{script: script{replies: []ai.Message{
		callTool("c1", "read", map[string]any{"path": "missing.txt"}),
		billed(say("done"), 10, 2, 0, 0),
	}}}
	r := &fakeRouter{p: p}
	a := newAgent(p, t.TempDir(), 0)
	a.SetRouter(r)

	var events []Event
	if _, err := a.PromptWith(context.Background(), "fix it", func(e Event) { events = append(events, e) }); err != nil {
		t.Fatal(err)
	}

	if len(r.reqs) != 2 || r.reqs[0].Reason != RouteUser || r.reqs[1].Reason != RouteContinuation {
		t.Fatalf("route requests = %+v", r.reqs)
	}
	if got := r.reqs[0]; got.Current.Model.ID != "test" || got.Messages[len(got.Messages)-1].Text() != "fix it" || got.ContextTokens == 0 {
		t.Errorf("first request = %+v", got)
	}
	if got := r.reqs[1].Current; got.Model.ID != "strong" || got.Options.Reasoning != "high" {
		t.Errorf("a continuation must start from the routed model, got %+v", got)
	}
	if strings.Join(p.models, ",") != "ollama/strong,ollama/strong" || strings.Join(p.efforts, ",") != "high,high" {
		t.Errorf("calls went to %v at %v", p.models, p.efforts)
	}

	var routes []Event
	for _, e := range events {
		if e.Type == EventRoute {
			routes = append(routes, e)
		}
	}
	if len(routes) != 1 || routes[0].Model != "ollama/strong" || routes[0].Effort != "high" || routes[0].Text != "implement, demanding" {
		t.Errorf("route events = %+v", routes)
	}

	st := a.Stats()
	if st.Context.Window != 1000 {
		t.Errorf("context window = %d, want the routed model's", st.Context.Window)
	}
	if st.Tokens.Input != 310 || st.Tokens.Cost != 0.25 {
		t.Errorf("totals = %+v, want the reply and the classifier", st.Tokens)
	}
	if len(st.ByModel) != 1 || st.ByModel[0].Ref != "laya/english" {
		t.Errorf("by model = %+v", st.ByModel)
	}
	if st.LastCacheHit != 0 {
		t.Errorf("cache hit = %v, want the reply's (0), not the classifier's", st.LastCacheHit)
	}
}

func TestRoutedModelAndChargeAreRecordedBeforeThePrompt(t *testing.T) {
	p := &seen{script: script{replies: []ai.Message{say("ok")}}}
	rec := &recorder{}
	a := newRecordedAgent(p, t.TempDir(), rec)
	a.SetRouter(&fakeRouter{p: p})
	if _, err := a.Prompt(context.Background(), "fix it"); err != nil {
		t.Fatal(err)
	}
	want := "charge laya/english,model ollama/strong,user,assistant"
	if got := strings.Join(rec.log, ","); got != want {
		t.Errorf("recorded %s\nwant     %s", got, want)
	}
}

func TestRouteWithoutNoteIsStillAnnounced(t *testing.T) {
	p := &seen{script: script{replies: []ai.Message{say("ok")}}}
	a := newAgent(p, t.TempDir(), 0)
	a.SetRouter(quietRouter{})
	var routes, warnings []Event
	if _, err := a.PromptWith(context.Background(), "ok", func(e Event) {
		switch e.Type {
		case EventRoute:
			routes = append(routes, e)
		case EventWarning:
			warnings = append(warnings, e)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || routes[0].Model != "ollama/test" || routes[0].Text != "" {
		t.Errorf("route events = %+v, want one naming the model, with no note", routes)
	}
	if len(warnings) != 1 || warnings[0].Text != "ollama is down" {
		t.Errorf("warnings = %+v", warnings)
	}
}

// quietRouter keeps every call where it is, with a warning and no note.
type quietRouter struct{}

func (quietRouter) Route(_ context.Context, req RouteRequest) (Route, error) {
	r := req.Current
	r.Warning = "ollama is down"
	return r, nil
}

func TestRouterFailureKeepsModel(t *testing.T) {
	p := &seen{script: script{replies: []ai.Message{say("ok")}}}
	r := &fakeRouter{p: p, err: errors.New("laya timed out")}
	a := newAgent(p, t.TempDir(), 0)
	a.SetRouter(r)

	var warnings []string
	_, err := a.PromptWith(context.Background(), "hi", func(e Event) {
		if e.Type == EventWarning {
			warnings = append(warnings, e.Text)
		}
	})
	if err != nil {
		t.Fatalf("a router failure must not fail the prompt: %v", err)
	}
	if strings.Join(p.models, ",") != "ollama/test" {
		t.Errorf("calls went to %v, want the configured model", p.models)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "laya timed out") {
		t.Errorf("warnings = %v", warnings)
	}
	if st := a.Stats(); st.Tokens.Cost != 0.5 {
		t.Errorf("cost = %v, want the failed router's charge counted", st.Tokens.Cost)
	}
}

func TestNoRouterNoRouting(t *testing.T) {
	p := &seen{script: script{replies: []ai.Message{say("ok"), say("again")}}}
	a := newAgent(p, t.TempDir(), 0)
	a.SetRouter(&fakeRouter{p: p})
	a.SetRouter(nil)
	if _, err := a.Prompt(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.models, ",") != "ollama/test" {
		t.Errorf("calls went to %v", p.models)
	}
}
