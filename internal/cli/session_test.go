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

func TestSessionChoicesMergesConfiguredAndReportsMissingLogin(t *testing.T) {
	// Nothing listens on this port: listing fails, models.json fills in.
	agentDir(t, `{"providers":{"ollama":{"baseUrl":"http://127.0.0.1:1/v1","models":[{"id":"b"},{"id":"a"}]}}}`)
	refs, err := newSession("", "").Choices(context.Background())

	if strings.Join(refs, ",") != "ollama/a,ollama/b" {
		t.Errorf("refs = %v", refs)
	}
	if err == nil || !strings.Contains(err.Error(), "ollama") || !strings.Contains(err.Error(), "openai-codex") {
		t.Errorf("err = %v, want warnings for ollama and the missing codex login", err)
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
	if err == nil || !strings.Contains(err.Error(), "ollama") {
		t.Errorf("err = %v, want a warning that ollama is logged out", err)
	}
}

func TestWithConfigured(t *testing.T) {
	got := withConfigured([]string{"b", "a"}, []string{"c", "a"})
	if strings.Join(got, ",") != "a,b,c" {
		t.Errorf("got %v", got)
	}
}

func TestToTUIEvent(t *testing.T) {
	got := toTUIEvent(agent.Event{Type: agent.EventTurnEnd, Usage: ai.Usage{Input: 100, CacheRead: 50, Output: 7}})
	if got.Kind != tui.EventUsage || got.Tokens != 157 {
		t.Errorf("usage event = %+v", got)
	}
	got = toTUIEvent(agent.Event{Type: agent.EventToolStart, Tool: "bash", Args: []byte(`{"command":"ls"}`)})
	if got.Kind != tui.EventToolStart || got.Tool != "bash" || got.Args != `{"command":"ls"}` {
		t.Errorf("tool event = %+v", got)
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
