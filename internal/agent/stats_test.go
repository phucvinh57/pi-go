package agent

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/phucvinh57/pi-go/internal/ai"
)

// billed returns reply with the usage a provider would have reported for it.
func billed(reply ai.Message, input, output, cacheRead int, cost float64) ai.Message {
	reply.Usage = ai.Usage{
		Input: input, Output: output, CacheRead: cacheRead,
		TotalTokens: input + output + cacheRead,
		Cost:        ai.Cost{Total: cost},
	}
	return reply
}

func failed(msg string, input int, cost float64) ai.Message {
	m := billed(ai.Message{Role: ai.RoleAssistant}, input, 0, 0, cost)
	m.StopReason, m.ErrorMessage = ai.StopError, msg
	return m
}

// recorder keeps what the agent hands it.
type recorder struct{ got []ai.Message }

func (r *recorder) Record(m ai.Message) { r.got = append(r.got, m) }

func (r *recorder) roles() []string {
	var s []string
	for _, m := range r.got {
		s = append(s, string(m.Role))
	}
	return s
}

func newRecordedAgent(p ai.Provider, dir string, r Recorder) *Agent {
	a := newAgent(p, dir, 0)
	a.cfg.Recorder = r
	return a
}

func TestTotalsAddUpAcrossTurns(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &script{replies: []ai.Message{
		billed(callTool("1", "read", map[string]any{"path": "a.txt"}), 100, 10, 0, 0.25),
		billed(say("done"), 150, 20, 100, 0.5),
	}}
	a := newAgent(p, dir, 0)
	if _, err := a.Prompt(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	s := a.Stats()
	if s.Tokens.Input != 250 || s.Tokens.Output != 30 || s.Tokens.CacheRead != 100 || s.Tokens.Total() != 380 {
		t.Errorf("tokens = %+v", s.Tokens)
	}
	if math.Abs(s.Tokens.Cost-0.75) > 1e-9 {
		t.Errorf("cost = %v, want 0.75", s.Tokens.Cost)
	}
	if s.UserMessages != 1 || s.AssistantMessages != 2 || s.ToolCalls != 1 || s.ToolResults != 1 {
		t.Errorf("counts = %+v", s)
	}
}

// A prompt that fails is rolled back out of the conversation, but its model
// calls were billed, so the totals keep them and the record shows them.
func TestFailedPromptKeepsBilledUsage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &script{replies: []ai.Message{
		billed(callTool("1", "read", map[string]any{"path": "a.txt"}), 100, 10, 0, 0.5),
		failed("boom", 200, 1),
	}}
	rec := &recorder{}
	a := newRecordedAgent(p, dir, rec)

	var last *Stats
	_, err := a.PromptWith(context.Background(), "go", func(e Event) {
		if e.Type == EventStats {
			last = e.Stats
		}
	})
	if err == nil {
		t.Fatal("want an error")
	}

	if n := len(a.Messages()); n != 0 {
		t.Errorf("conversation has %d messages, want 0 after rollback", n)
	}
	s := a.Stats()
	if s.Tokens.Input != 300 || math.Abs(s.Tokens.Cost-1.5) > 1e-9 {
		t.Errorf("totals after rollback = %+v, want input 300 cost 1.5", s.Tokens)
	}
	if s.UserMessages != 0 || s.AssistantMessages != 0 {
		t.Errorf("counts after rollback = %+v", s)
	}

	// The last event tells the UI the conversation shrank.
	if last == nil || last.UserMessages != 0 || last.Tokens.Input != 300 {
		t.Errorf("last stats event = %+v", last)
	}

	want := []string{"user", "assistant", "toolResult", "assistant"}
	got := rec.roles()
	if len(got) != len(want) {
		t.Fatalf("recorded %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("recorded %v, want %v", got, want)
		}
	}
	if end := rec.got[3]; end.StopReason != ai.StopError || end.ErrorMessage != "boom" {
		t.Errorf("the failed call was recorded as %+v", end)
	}
}

func TestRecorderSeesTheConversationInOrder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &script{replies: []ai.Message{
		callTool("1", "read", map[string]any{"path": "a.txt"}),
		say("done"),
	}}
	rec := &recorder{}
	a := newRecordedAgent(p, dir, rec)
	if _, err := a.Prompt(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	got := rec.roles()
	want := []string{"user", "assistant", "toolResult", "assistant"}
	if len(got) != len(want) {
		t.Fatalf("recorded %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("recorded %v, want %v", got, want)
		}
	}
	if rec.got[0].Text() != "go" || rec.got[2].Text() != "secret" || rec.got[3].Text() != "done" {
		t.Errorf("recorded content = %q %q %q", rec.got[0].Text(), rec.got[2].Text(), rec.got[3].Text())
	}
}

func TestNoRecorderIsFine(t *testing.T) {
	p := &script{replies: []ai.Message{say("hi")}}
	a := newAgent(p, t.TempDir(), 0) // Config.Recorder is nil
	if _, err := a.Prompt(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
}

func TestContextEstimate(t *testing.T) {
	user := ai.UserText("abcd")                                            // 4 chars: 1 token
	reply := billed(say("hi"), 400, 100, 0, 0)                             // the provider counted 500
	result := ai.ToolResult("1", "read", string(make([]byte, 400)), false) // 400 chars: 100 tokens

	a := newAgent(&script{}, t.TempDir(), 0)
	a.cfg.Model.ContextWindow = 1000
	a.messages = []ai.Message{user, reply, result}

	c := a.Stats().Context
	if c.Tokens != 600 || c.Window != 1000 || math.Abs(c.Percent-60) > 1e-9 {
		t.Errorf("context = %+v, want 500 reported + 100 estimated of 1000", c)
	}

	// With nothing reported, everything is estimated, system prompt included.
	a.messages = []ai.Message{ai.UserText("12345678")} // 2 tokens
	if got := a.Stats().Context.Tokens; got != 1+2 {   // "sys" is 1
		t.Errorf("estimate without usage = %d, want 3", got)
	}
}

func TestContextWindowUnknown(t *testing.T) {
	a := newAgent(&script{}, t.TempDir(), 0)
	a.messages = []ai.Message{ai.UserText("12345678")}
	c := a.Stats().Context
	if c.Window != 0 || c.Percent != 0 || c.Tokens == 0 {
		t.Errorf("context = %+v: want tokens but no percentage", c)
	}
}

func TestCacheHitIsTheLastCall(t *testing.T) {
	a := newAgent(&script{replies: []ai.Message{
		billed(say("a"), 1000, 10, 0, 0),
		billed(say("b"), 100, 10, 300, 0),
	}}, t.TempDir(), 0)

	if got := a.Stats().LastCacheHit; got != -1 {
		t.Errorf("before any call = %v, want -1", got)
	}
	for _, text := range []string{"1", "2"} {
		if _, err := a.Prompt(context.Background(), text); err != nil {
			t.Fatal(err)
		}
	}
	if got := a.Stats().LastCacheHit; math.Abs(got-75) > 1e-9 {
		t.Errorf("cache hit = %v, want 75 (300 of 400 prompt tokens)", got)
	}
}

func TestByModelAndSubscriptionFollowSetModel(t *testing.T) {
	p := &script{replies: []ai.Message{
		billed(say("a"), 10, 1, 0, 1),
		billed(say("b"), 10, 1, 0, 3),
		billed(say("c"), 10, 1, 0, 0), // free: left out of the breakdown
	}}
	a := newAgent(p, t.TempDir(), 0)

	run := func(text string) {
		t.Helper()
		if _, err := a.Prompt(context.Background(), text); err != nil {
			t.Fatal(err)
		}
	}
	run("1") // ollama/test
	a.SetModel(p, ai.Model{Provider: "openai-codex", ID: "gpt"}, ai.Options{}, true)
	run("2")
	a.SetModel(p, ai.Model{Provider: "ollama", ID: "free"}, ai.Options{}, false)
	run("3")

	s := a.Stats()
	if len(s.ByModel) != 2 || s.ByModel[0].Ref != "openai-codex/gpt" || s.ByModel[1].Ref != "ollama/test" {
		t.Errorf("by model = %+v, want codex (3) before test (1), no free model", s.ByModel)
	}
	if s.Subscription {
		t.Error("the active model is not a subscription")
	}
	if s.Tokens.Cost != 4 {
		t.Errorf("cost = %v, want 4", s.Tokens.Cost)
	}
	if s.UserMessages != 3 {
		t.Errorf("the conversation must survive model switches: %d user messages", s.UserMessages)
	}

	a.SetModel(p, ai.Model{Provider: "openai-codex", ID: "gpt"}, ai.Options{}, true)
	if !a.Stats().Subscription {
		t.Error("Subscription must follow SetModel")
	}
}

// The stats event must describe the conversation including the reply that
// caused it: a UI showing it has nothing else to go on.
func TestStatsEventIncludesTheReply(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &script{replies: []ai.Message{
		billed(callTool("1", "read", map[string]any{"path": "a.txt"}), 100, 10, 0, 0),
		billed(say("done"), 300, 20, 0, 0),
	}}
	a := newAgent(p, dir, 0)
	a.cfg.Model.ContextWindow = 1000

	var events []*Stats
	if _, err := a.PromptWith(context.Background(), "go", func(e Event) {
		if e.Type == EventStats {
			events = append(events, e.Stats)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("%d stats events, want one per model call", len(events))
	}

	// After the first call: the reply (a tool call) is in, the tool result is not.
	if first := events[0]; first.AssistantMessages != 1 || first.Context.Tokens != 110 {
		t.Errorf("first = %d assistant messages, context %d; want 1 and 110", first.AssistantMessages, first.Context.Tokens)
	}
	// After the last call the event must equal what Stats reports now.
	final := a.Stats()
	last := events[1]
	if last.AssistantMessages != 2 || last.Context.Tokens != 320 || last.Context != final.Context || last.Tokens != final.Tokens {
		t.Errorf("last event = %+v\nStats()    = %+v", last, final)
	}
}
