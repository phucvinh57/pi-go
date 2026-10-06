package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/phucvinh57/pi-go/internal/ai"
	sessionfile "github.com/phucvinh57/pi-go/internal/session"
	"github.com/phucvinh57/pi-go/internal/tui"
)

// fakeOllama serves the models qwen and other; see fakeOllamaListing.
func fakeOllama(t *testing.T) string {
	t.Helper()
	return fakeOllamaListing(t, "qwen", "other")
}

// fakeOllamaListing lists ids as its models, and answers every chat completion
// with "hello" and fixed usage: 12 prompt tokens of which 2 were cached, and 3
// completion tokens.
func fakeOllamaListing(t *testing.T, ids ...string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
			var data []map[string]string
			for _, id := range ids {
				data = append(data, map[string]string{"id": id})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, l := range []string{
			`{"choices":[{"delta":{"content":"hello"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":2}}}`,
			`[DONE]`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", l)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/v1"
}

// recordingSetup points an agent dir at the fake server with a model priced at
// $1 per input token and $2 per output token, so costs are easy to check. Both
// models can reason, so auto effort applies to them.
func recordingSetup(t *testing.T) environment {
	t.Helper()
	url := fakeOllama(t)
	dir := t.TempDir()
	models := fmt.Sprintf(`{"providers":{"ollama":{"baseUrl":%q,"models":[
		{"id":"qwen","contextWindow":1000,"cost":{"input":1000000,"output":2000000},"reasoning":true},
		{"id":"other","reasoning":true}
	]}}}`, url)
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	return environment{agentDir: dir, cwd: t.TempDir()}
}

func collectEvents(emit *[]tui.Event) func(tui.Event) {
	return func(e tui.Event) { *emit = append(*emit, e) }
}

func lastStats(events []tui.Event) *tui.Stats {
	var s *tui.Stats
	for _, e := range events {
		if e.Kind == tui.EventStats {
			s = e.Stats
		}
	}
	return s
}

func TestPromptSavesTheSessionAndReportsStats(t *testing.T) {
	env := recordingSetup(t)
	dir := env.agentDir
	s := newSession(env, "", "ollama/qwen")

	var events []tui.Event
	if err := s.Prompt(context.Background(), "say hello", collectEvents(&events)); err != nil {
		t.Fatal(err)
	}

	st := lastStats(events)
	if st == nil {
		t.Fatal("no stats event")
	}
	// Input excludes the 2 cached tokens; cost is 10*$1 + 3*$2.
	if st.Input != 10 || st.CacheRead != 2 || st.Output != 3 || st.Cost != 16 {
		t.Errorf("stats = %+v", st)
	}
	if st.ContextWindow != 1000 || st.ContextTokens != 15 || st.ContextPercent != 1.5 {
		t.Errorf("context = %d tokens of %d (%.1f%%)", st.ContextTokens, st.ContextWindow, st.ContextPercent)
	}
	if st.Subscription || st.UserMessages != 1 || st.AssistantMessages != 1 {
		t.Errorf("stats = %+v", st)
	}

	sessions, err := sessionfile.LoadAll(dir)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %v, err %v", sessions, err)
	}
	saved := sessions[0]
	cwd := env.cwd
	if saved.Header.CWD != cwd || filepath.Dir(saved.Path) != sessionfile.Dir(dir, cwd) {
		t.Errorf("saved for %q at %s", saved.Header.CWD, saved.Path)
	}
	if len(saved.Entries) != 3 {
		t.Fatalf("entries = %+v", saved.Entries)
	}
	if e := saved.Entries[0]; e.Type != sessionfile.TypeModelChange || e.Provider != "ollama" || e.ModelID != "qwen" {
		t.Errorf("first entry = %+v", e)
	}
	if m := saved.Entries[1].Message; m == nil || m.Role != ai.RoleUser || m.Text() != "say hello" {
		t.Errorf("user entry = %+v", saved.Entries[1])
	}
	reply := saved.Entries[2].Message
	if reply == nil || reply.Text() != "hello" || reply.Usage.Input != 10 || reply.Usage.Cost.Total != 16 {
		t.Errorf("assistant entry = %+v", reply)
	}
}

func TestSwitchingModelsIsSaved(t *testing.T) {
	env := recordingSetup(t)
	dir := env.agentDir
	s := newSession(env, "", "ollama/qwen")
	if err := s.Prompt(context.Background(), "hi", func(tui.Event) {}); err != nil {
		t.Fatal(err)
	}
	if err := s.Select(context.Background(), "ollama/other"); err != nil {
		t.Fatal(err)
	}
	if err := s.Prompt(context.Background(), "again", func(tui.Event) {}); err != nil {
		t.Fatal(err)
	}

	sessions, _ := sessionfile.LoadAll(dir)
	if len(sessions) != 1 {
		t.Fatalf("a model switch must stay in the same session, got %d sessions", len(sessions))
	}
	var changes []string
	for _, e := range sessions[0].Entries {
		if e.Type == sessionfile.TypeModelChange {
			changes = append(changes, e.ModelID)
		}
	}
	if len(changes) != 2 || changes[0] != "qwen" || changes[1] != "other" {
		t.Errorf("model changes = %v", changes)
	}
}

func TestNoSessionSavesNothing(t *testing.T) {
	env := recordingSetup(t)
	dir := env.agentDir
	s := newSession(env, "", "ollama/qwen")
	s.conv.noSession = true

	var events []tui.Event
	if err := s.Prompt(context.Background(), "private", collectEvents(&events)); err != nil {
		t.Fatal(err)
	}
	if paths, _ := sessionfile.List(dir); len(paths) != 0 {
		t.Errorf("--no-session saved %v", paths)
	}
	if err := s.SaveError(); err != nil {
		t.Errorf("SaveError = %v", err)
	}
	// The footer still works: only the saving is off.
	if st := lastStats(events); st == nil || st.Input != 10 {
		t.Errorf("stats = %+v", st)
	}
}

func TestSaveFailureIsReportedOnceAndDoesNotFailThePrompt(t *testing.T) {
	env := recordingSetup(t)
	dir := env.agentDir
	// A file where the sessions directory belongs makes saving impossible.
	if err := os.WriteFile(filepath.Join(dir, "sessions"), []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newSession(env, "", "ollama/qwen")

	warnings := func(events []tui.Event) (n int) {
		for _, e := range events {
			if e.Kind == tui.EventWarning {
				n++
			}
		}
		return n
	}

	var first []tui.Event
	if err := s.Prompt(context.Background(), "one", collectEvents(&first)); err != nil {
		t.Fatalf("a failure to save must not fail the prompt: %v", err)
	}
	if warnings(first) != 1 {
		t.Fatalf("first prompt: %d warnings, want 1", warnings(first))
	}
	for _, e := range first {
		if e.Kind == tui.EventWarning && e.Text == "" {
			t.Error("the warning has no text")
		}
	}

	var second []tui.Event
	if err := s.Prompt(context.Background(), "two", collectEvents(&second)); err != nil {
		t.Fatal(err)
	}
	if warnings(second) != 0 {
		t.Errorf("the warning was repeated: %d", warnings(second))
	}
	if st := lastStats(second); st == nil || st.UserMessages != 2 {
		t.Errorf("the conversation must carry on: %+v", st)
	}
}

func TestSaveErrorForPrintMode(t *testing.T) {
	env := recordingSetup(t)
	dir := env.agentDir
	if err := os.WriteFile(filepath.Join(dir, "sessions"), []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newSession(env, "", "ollama/qwen")
	if _, err := s.Respond(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveError(); err == nil {
		t.Fatal("want the save error")
	}
	if err := s.SaveError(); err != nil {
		t.Errorf("reported twice: %v", err)
	}
}
