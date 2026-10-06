package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phucvinh57/pi-go/internal/config"
	"github.com/phucvinh57/pi-go/internal/settings"

	"github.com/phucvinh57/pi-go/internal/agent"
	"github.com/phucvinh57/pi-go/internal/ai"
	"github.com/phucvinh57/pi-go/internal/auth"
	"github.com/phucvinh57/pi-go/internal/tui"
)

func TestSessionCurrentNormalizesBareID(t *testing.T) {
	agentDir(t, `{"providers":{"ollama":{"models":[{"id":"qwen2.5:7b"}]}}}`)
	if got := newSession("", "qwen2.5:7b").Current(); got != "ollama/qwen2.5:7b" {
		t.Errorf("Current = %q", got)
	}
	if got := newSession("", "nope/x").Current(); got != "nope/x" {
		t.Errorf("an unusable model should come back as given, got %q", got)
	}
	if got := newSession("", "").Current(); got != DefaultModel {
		t.Errorf("default = %q", got)
	}
}

func TestSessionSelectBeforeAgentExists(t *testing.T) {
	agentDir(t, "")
	s := newSession("", "")
	if err := s.Select(context.Background(), "ollama/llama3.2:latest"); err != nil {
		t.Fatal(err)
	}
	if got := s.Current(); got != "ollama/llama3.2:latest" {
		t.Errorf("Current = %q", got)
	}
}

func TestSessionSelectSwitchesRunningAgent(t *testing.T) {
	agentDir(t, "")
	s := newSession("", "")
	s.agent = agent.New(agent.Config{Model: ai.Model{Provider: "ollama", ID: "old"}})

	if err := s.Select(context.Background(), "ollama/new"); err != nil {
		t.Fatal(err)
	}
	if s.agent == nil {
		t.Fatal("the agent, and with it the conversation, must be kept")
	}
	if got := s.Current(); got != "ollama/new" {
		t.Errorf("Current = %q", got)
	}
}

func TestSessionSelectFailureKeepsModel(t *testing.T) {
	agentDir(t, "") // no login for openai-codex
	s := newSession("", "ollama/a")
	if err := s.Select(context.Background(), "openai-codex/gpt-5.5"); err == nil {
		t.Fatal("selecting a provider without credentials succeeded")
	}
	if got := s.Current(); got != "ollama/a" {
		t.Errorf("Current = %q after a failed switch", got)
	}
}

func TestSessionChoicesMergesConfiguredAndSkipsMissingLogin(t *testing.T) {
	// Nothing listens on this port: listing fails, models.json fills in.
	agentDir(t, `{"providers":{"ollama":{"baseUrl":"http://127.0.0.1:1/v1","models":[{"id":"b"},{"id":"a"}]}}}`)
	refs, err := newSession("", "").Choices(context.Background())

	if strings.Join(refs, ",") != "ollama/a,ollama/b" {
		t.Errorf("refs = %v", refs)
	}
	if err == nil || !strings.Contains(err.Error(), "ollama") {
		t.Errorf("err = %v, want a warning that ollama cannot be listed", err)
	}
	if err != nil && strings.Contains(err.Error(), "openai-codex") {
		t.Errorf("err = %v, a provider that is not logged in must not be reported", err)
	}
}

func TestSessionChoicesSkipsLoggedOutOllama(t *testing.T) {
	agentDir(t, `{"providers":{"ollama":{"baseUrl":"http://127.0.0.1:1/v1","models":[{"id":"a"}]}}}`)
	t.Setenv("OLLAMA_API_KEY", "")
	if _, err := auth.Logout("ollama"); err != nil {
		t.Fatal(err)
	}

	refs, err := newSession("", "").Choices(context.Background())
	if len(refs) != 0 {
		t.Errorf("refs = %v, want no models after logging out of ollama", refs)
	}
	if err != nil {
		t.Errorf("err = %v, want no warning for a provider that is logged out", err)
	}
}

func TestWithConfigured(t *testing.T) {
	got := withConfigured([]string{"b", "a"}, []string{"c", "a"})
	if strings.Join(got, ",") != "a,b,c" {
		t.Errorf("got %v", got)
	}
}

func TestToTUIEvent(t *testing.T) {
	// A model call finishing is reported through the stats event; the TUI has
	// no use for the raw one, and must not see it as empty reply text.
	if got, ok := toTUIEvent(agent.Event{Type: agent.EventTurnEnd, Usage: ai.Usage{Input: 100, Output: 7}}); ok {
		t.Errorf("turn end was passed on as %+v", got)
	}

	got, ok := toTUIEvent(agent.Event{Type: agent.EventToolStart, Tool: "bash", Args: []byte(`{"command":"ls"}`)})
	if !ok || got.Kind != tui.EventToolStart || got.Tool != "bash" || got.Args != `{"command":"ls"}` {
		t.Errorf("tool event = %+v", got)
	}
}

func TestToTUIEventStats(t *testing.T) {
	got, ok := toTUIEvent(agent.Event{Type: agent.EventStats, Stats: &agent.Stats{
		UserMessages: 2, AssistantMessages: 3, ToolCalls: 1, ToolResults: 1,
		Tokens:       agent.Totals{Input: 100, Output: 7, CacheRead: 50, CacheWrite: 5, Cost: 0.25},
		ByModel:      []agent.ModelCost{{Ref: "a/b", Cost: 0.25}},
		LastCacheHit: 33.5,
		Subscription: true,
		Context:      agent.ContextUsage{Tokens: 162, Window: 1000, Percent: 16.2},
	}})
	if !ok || got.Kind != tui.EventStats || got.Stats == nil {
		t.Fatalf("event = %+v, %v", got, ok)
	}
	want := tui.Stats{
		UserMessages: 2, AssistantMessages: 3, ToolCalls: 1, ToolResults: 1,
		Input: 100, Output: 7, CacheRead: 50, CacheWrite: 5, Cost: 0.25,
		ByModel:  []tui.ModelCost{{Ref: "a/b", Cost: 0.25}},
		CacheHit: 33.5, Subscription: true,
		ContextTokens: 162, ContextWindow: 1000, ContextPercent: 16.2,
	}
	if g := *got.Stats; g.Input != want.Input || g.Output != want.Output || g.CacheRead != want.CacheRead ||
		g.CacheWrite != want.CacheWrite || g.Cost != want.Cost || g.CacheHit != want.CacheHit ||
		g.Subscription != want.Subscription || g.ContextTokens != want.ContextTokens ||
		g.ContextWindow != want.ContextWindow || g.ContextPercent != want.ContextPercent ||
		g.UserMessages != want.UserMessages || g.AssistantMessages != want.AssistantMessages ||
		g.ToolCalls != want.ToolCalls || g.ToolResults != want.ToolResults ||
		len(g.ByModel) != 1 || g.ByModel[0] != want.ByModel[0] {
		t.Errorf("stats = %+v\nwant    %+v", g, want)
	}

	if got, ok := toTUIEvent(agent.Event{Type: agent.EventStats}); !ok || got.Stats != nil {
		t.Errorf("a stats event without stats = %+v, %v", got, ok)
	}
}

func TestSessionStartsOnSavedDefault(t *testing.T) {
	agentDir(t, `{"providers":{"ollama":{"models":[{"id":"saved"}]}}}`)
	if err := settings.SetDefaultModel("ollama/saved"); err != nil {
		t.Fatal(err)
	}
	if got := newSession("", "").Current(); got != "ollama/saved" {
		t.Errorf("Current = %q, want the saved default", got)
	}
	if got := newSession("", "ollama/flag").Current(); got != "ollama/flag" {
		t.Errorf("Current = %q, --model must win over the saved default", got)
	}
}

func TestSessionSetDefaultSavesAndSwitches(t *testing.T) {
	agentDir(t, "")
	s := newSession("", "")
	if err := s.SetDefault(context.Background(), "ollama/new"); err != nil {
		t.Fatal(err)
	}
	if got := s.Current(); got != "ollama/new" {
		t.Errorf("Current = %q", got)
	}
	if got, _ := settings.DefaultModel(); got != "ollama/new" {
		t.Errorf("saved = %q", got)
	}
}

func TestSessionSetDefaultFailureSavesNothing(t *testing.T) {
	agentDir(t, "") // no login for openai-codex
	s := newSession("", "ollama/a")
	if err := s.SetDefault(context.Background(), "openai-codex/gpt-5.5"); err == nil {
		t.Fatal("selecting a provider without credentials succeeded")
	}
	if got, _ := settings.DefaultModel(); got != "" {
		t.Errorf("saved = %q after a failed switch", got)
	}
}

func TestSessionReportsUnreadableSettingsOnFirstPrompt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.AgentDirEnv, dir)
	if err := os.WriteFile(filepath.Join(dir, "settings.toml"), []byte("default_model = "), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newSession("", "")
	if _, err := s.Respond(context.Background(), "hi"); err == nil || !strings.Contains(err.Error(), "settings.toml") {
		t.Errorf("err = %v, want it to name settings.toml", err)
	}
}

func TestSessionEffortReachesAgentAndSurvivesModelSwitch(t *testing.T) {
	agentDir(t, "")
	s := newSession("", "")
	s.agent = agent.New(agent.Config{Model: ai.Model{Provider: "ollama", ID: "old"}})

	if err := s.SetEffort("high"); err != nil {
		t.Fatal(err)
	}
	if got := s.Effort(); got != "high" {
		t.Errorf("Effort = %q", got)
	}
	if err := s.Select(context.Background(), "ollama/new"); err != nil {
		t.Fatal(err)
	}
	if got := s.Effort(); got != "high" {
		t.Errorf("Effort after /model = %q, want it kept", got)
	}
	if err := s.SetEffort("max"); err != nil || s.Effort() != "max" {
		t.Errorf("setting max: err=%v effort=%q", err, s.Effort())
	}
	if err := s.SetEffort("turbo"); err == nil {
		t.Error("an unknown effort must be refused")
	}
	if got := s.Effort(); got != "max" {
		t.Errorf("Effort after a refused value = %q", got)
	}
	for _, level := range []string{"", "default"} {
		if err := s.SetEffort(level); err == nil || s.Effort() != "max" {
			t.Errorf("setting %q: err=%v effort=%q; want refusal and max unchanged", level, err, s.Effort())
		}
	}
}
