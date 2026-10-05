package cli

import (
	"context"
	"strings"
	"testing"

	"pi-go/internal/agent"
	"pi-go/internal/ai"
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

func TestWithConfigured(t *testing.T) {
	got := withConfigured([]string{"b", "a"}, []string{"c", "a"})
	if strings.Join(got, ",") != "a,b,c" {
		t.Errorf("got %v", got)
	}
}
