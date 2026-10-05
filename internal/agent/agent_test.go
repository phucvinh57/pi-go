package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pi-go/internal/ai"
	"pi-go/internal/tools"
)

// script is an ai.Provider that plays back canned assistant messages and
// records the context of every request.
type script struct {
	replies  []ai.Message
	requests []ai.Context
}

func (s *script) Stream(_ context.Context, _ ai.Model, c ai.Context, _ ai.Options) <-chan ai.Event {
	// Copy: the agent keeps appending to its slice after the call returns.
	c.Messages = append([]ai.Message(nil), c.Messages...)
	s.requests = append(s.requests, c)

	ch := make(chan ai.Event, 1)
	if len(s.replies) == 0 {
		ch <- ai.Event{Type: ai.EventError, Message: &ai.Message{Role: ai.RoleAssistant, StopReason: ai.StopError, ErrorMessage: "script exhausted"}}
	} else {
		reply := s.replies[0]
		s.replies = s.replies[1:]
		ch <- ai.Event{Type: ai.EventDone, Message: &reply}
	}
	close(ch)
	return ch
}

func say(text string) ai.Message {
	return ai.Message{Role: ai.RoleAssistant, StopReason: ai.StopStop, Content: []ai.Block{{Type: ai.BlockText, Text: text}}}
}

func callTool(id, name string, args any) ai.Message {
	raw, _ := json.Marshal(args)
	return ai.Message{Role: ai.RoleAssistant, StopReason: ai.StopToolUse, Content: []ai.Block{{Type: ai.BlockToolCall, ID: id, Name: name, Arguments: raw}}}
}

func newAgent(p ai.Provider, dir string, maxTurns int) *Agent {
	return New(Config{
		Provider:     p,
		Model:        ai.Model{Provider: "ollama", ID: "test"},
		SystemPrompt: "sys",
		Tools:        tools.Core(dir),
		MaxTurns:     maxTurns,
	})
}

func TestPromptWithoutTools(t *testing.T) {
	p := &script{replies: []ai.Message{say("hello")}}
	a := newAgent(p, t.TempDir(), 0)

	reply, err := a.Prompt(context.Background(), "hi")
	if err != nil || reply.Text() != "hello" {
		t.Fatalf("got %q, %v", reply.Text(), err)
	}
	req := p.requests[0]
	if req.SystemPrompt != "sys" || len(req.Tools) != 4 || req.Messages[0].Text() != "hi" {
		t.Errorf("request = %+v", req)
	}
	if req.Tools[0].Name != "read" || !json.Valid(req.Tools[0].Parameters) {
		t.Errorf("tool declaration = %+v", req.Tools[0])
	}
}

func TestToolRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &script{replies: []ai.Message{
		callTool("c1", "read", map[string]any{"path": "a.txt"}),
		say("the file says secret"),
	}}
	a := newAgent(p, dir, 0)

	reply, err := a.Prompt(context.Background(), "what is in a.txt?")
	if err != nil || reply.Text() != "the file says secret" {
		t.Fatalf("got %q, %v", reply.Text(), err)
	}

	// The second request carries user, assistant (call) and the tool result.
	msgs := p.requests[1].Messages
	if len(msgs) != 3 {
		t.Fatalf("second request has %d messages", len(msgs))
	}
	res := msgs[2]
	if res.Role != ai.RoleToolResult || res.ToolCallID != "c1" || res.ToolName != "read" || res.Text() != "secret" || res.IsError {
		t.Errorf("tool result = %+v", res)
	}
	if n := len(a.Messages()); n != 4 {
		t.Errorf("conversation has %d messages, want 4", n)
	}
}

func TestToolsCanChange(t *testing.T) {
	dir := t.TempDir()
	p := &script{replies: []ai.Message{
		callTool("c1", "write", map[string]any{"path": "x/new.txt", "content": "v1\n"}),
		callTool("c2", "edit", map[string]any{"path": "x/new.txt", "edits": []map[string]string{{"oldText": "v1", "newText": "v2"}}}),
		callTool("c3", "bash", map[string]any{"command": "cat x/new.txt"}),
		say("done"),
	}}
	a := newAgent(p, dir, 0)
	if _, err := a.Prompt(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	last := p.requests[3].Messages
	if got := last[len(last)-1].Text(); got != "v2\n" {
		t.Errorf("bash output = %q", got)
	}
}

func TestToolFailuresReachTheModel(t *testing.T) {
	p := &script{replies: []ai.Message{
		callTool("c1", "read", map[string]any{"path": "missing.txt"}),
		callTool("c2", "teleport", map[string]any{}),
		say("sorry"),
	}}
	a := newAgent(p, t.TempDir(), 0)
	if _, err := a.Prompt(context.Background(), "go"); err != nil {
		t.Fatalf("a failing tool must not end the loop: %v", err)
	}
	msgs := a.Messages()
	if r := msgs[2]; !r.IsError || !strings.Contains(r.Text(), "missing.txt") {
		t.Errorf("read failure = %+v", r)
	}
	if r := msgs[4]; !r.IsError || r.Text() != `Unknown tool "teleport"` {
		t.Errorf("unknown tool = %+v", r)
	}
}

func TestSeveralCallsInOneTurn(t *testing.T) {
	dir := t.TempDir()
	two := ai.Message{Role: ai.RoleAssistant, StopReason: ai.StopToolUse, Content: []ai.Block{
		{Type: ai.BlockToolCall, ID: "a", Name: "bash", Arguments: json.RawMessage(`{"command":"echo 1"}`)},
		{Type: ai.BlockToolCall, ID: "b", Name: "bash", Arguments: json.RawMessage(`{"command":"echo 2"}`)},
	}}
	p := &script{replies: []ai.Message{two, say("ok")}}
	a := newAgent(p, dir, 0)
	if _, err := a.Prompt(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	msgs := p.requests[1].Messages
	if len(msgs) != 4 || msgs[2].ToolCallID != "a" || msgs[2].Text() != "1\n" || msgs[3].ToolCallID != "b" || msgs[3].Text() != "2\n" {
		t.Errorf("results out of order or missing: %+v", msgs)
	}
}

func TestConversationContinues(t *testing.T) {
	p := &script{replies: []ai.Message{say("one"), say("two")}}
	a := newAgent(p, t.TempDir(), 0)
	for _, q := range []string{"first", "second"} {
		if _, err := a.Prompt(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	msgs := p.requests[1].Messages
	if len(msgs) != 3 || msgs[0].Text() != "first" || msgs[1].Text() != "one" || msgs[2].Text() != "second" {
		t.Errorf("second request = %+v", msgs)
	}
}

func TestErrorRollsBack(t *testing.T) {
	p := &script{replies: []ai.Message{say("fine")}}
	a := newAgent(p, t.TempDir(), 0)
	if _, err := a.Prompt(context.Background(), "ok"); err != nil {
		t.Fatal(err)
	}
	before := len(a.Messages())

	// The script is now empty, so the model call fails.
	_, err := a.Prompt(context.Background(), "boom")
	if err == nil || !strings.Contains(err.Error(), "script exhausted") || !strings.Contains(err.Error(), "ollama/test") {
		t.Fatalf("err = %v", err)
	}
	if got := len(a.Messages()); got != before {
		t.Errorf("conversation has %d messages after a failed prompt, want %d", got, before)
	}
}

func TestMaxTurns(t *testing.T) {
	loop := callTool("c", "bash", map[string]any{"command": "true"})
	p := &script{replies: []ai.Message{loop, loop, loop, loop}}
	a := newAgent(p, t.TempDir(), 3)

	_, err := a.Prompt(context.Background(), "go")
	if err == nil || !strings.Contains(err.Error(), "3 model calls") {
		t.Fatalf("err = %v", err)
	}
	if len(p.requests) != 3 || len(a.Messages()) != 0 {
		t.Errorf("requests = %d, messages = %d", len(p.requests), len(a.Messages()))
	}
}

func TestCancelStopsLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := &script{replies: []ai.Message{
		callTool("c1", "bash", map[string]any{"command": "sleep 30"}),
		say("never"),
	}}
	a := newAgent(p, t.TempDir(), 0)
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := a.Prompt(ctx, "go")
	if err == nil || time.Since(start) > 10*time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
	if len(p.requests) != 1 || len(a.Messages()) != 0 {
		t.Errorf("requests = %d, messages = %d", len(p.requests), len(a.Messages()))
	}
}

func TestSystemPrompt(t *testing.T) {
	now := testNow
	got := SystemPrompt(tools.Core("/work"), "/work", now)
	for _, want := range []string{
		"- read: Read file contents",
		"- bash: Execute bash commands",
		"- edit: Make precise file edits",
		"- write: Create or overwrite files",
		"Use read to examine files instead of cat or sed.",
		"Current date: 2026-10-05",
		"Current working directory: /work",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q:\n%s", want, got)
		}
	}

	// Only the tools that are on appear.
	only := SystemPrompt([]tools.Tool{tools.NewRead("/w")}, "/w", now)
	if strings.Contains(only, "bash") || strings.Contains(only, "- write:") {
		t.Errorf("prompt lists tools that are off:\n%s", only)
	}
}

var testNow = time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)

func TestPromptWithReportsToolCalls(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &script{replies: []ai.Message{callTool("1", "read", map[string]any{"path": "a.txt"}), say("done")}}
	a := newAgent(p, dir, 0)

	var got []Event
	if _, err := a.PromptWith(context.Background(), "go", func(e Event) { got = append(got, e) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Type != EventToolStart || got[0].Tool != "read" || got[1].Type != EventToolEnd || !strings.Contains(got[1].Text, "secret") || got[1].IsError {
		t.Errorf("events = %+v", got)
	}
}

func TestPromptWithStreamsText(t *testing.T) {
	var sp streamScript
	a := newAgent(sp, t.TempDir(), 0)

	var text strings.Builder
	if _, err := a.PromptWith(context.Background(), "hi", func(e Event) {
		if e.Type == EventText {
			text.WriteString(e.Text)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if text.String() != "hello" {
		t.Errorf("streamed %q", text.String())
	}
}

// streamScript streams "hel" and "lo" as two deltas.
type streamScript struct{}

func (streamScript) Stream(context.Context, ai.Model, ai.Context, ai.Options) <-chan ai.Event {
	reply := say("hello")
	ch := make(chan ai.Event, 4)
	ch <- ai.Event{Type: ai.EventTextDelta, Delta: "hel"}
	ch <- ai.Event{Type: ai.EventTextDelta, Delta: "lo"}
	ch <- ai.Event{Type: ai.EventDone, Message: &reply}
	close(ch)
	return ch
}
