package cli

import (
	"os"
	"path/filepath"
	"testing"

	"pi-go/internal/ai"
	"pi-go/internal/config"
)

func agentDir(t *testing.T, modelsJSON string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(config.AgentDirEnv, dir)
	if modelsJSON != "" {
		if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(modelsJSON), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResolveModelOllama(t *testing.T) {
	agentDir(t, `{"providers":{"ollama":{"baseUrl":"http://gpu-box:11434/v1","models":[{"id":"qwen2.5:7b"}]}}}`)

	for _, ref := range []string{"ollama/qwen2.5:7b", "qwen2.5:7b"} {
		got, err := resolveModel("", ref)
		if err != nil {
			t.Fatalf("%s: %v", ref, err)
		}
		if got.Model.Ref() != "ollama/qwen2.5:7b" || got.Model.BaseURL != "http://gpu-box:11434/v1" || got.Model.API != ai.APICompletions {
			t.Errorf("%s: model = %+v", ref, got.Model)
		}
		if got.Options.APIKey != "ollama" || got.Provider == nil {
			t.Errorf("%s: options = %+v", ref, got.Options)
		}
	}
}

func TestResolveModelKeepsSlashesInID(t *testing.T) {
	agentDir(t, "")
	got, err := resolveModel("ollama", "hf.co/org/model")
	if err != nil || got.Model.ID != "hf.co/org/model" {
		t.Fatalf("got %+v err %v", got.Model, err)
	}
	if got.Model.BaseURL != "http://localhost:11434/v1" {
		t.Errorf("base URL = %q", got.Model.BaseURL)
	}
}

func TestResolveModelErrors(t *testing.T) {
	agentDir(t, "")

	if _, err := resolveModel("", "nope/x"); err == nil {
		t.Error("unknown provider accepted")
	}
	if _, err := resolveModel("", "bare-model"); err == nil {
		t.Error("model with no provider accepted")
	}
	// Approved by auth, but no model adapter yet.
	if _, err := resolveModel("openai", "gpt-4o"); err == nil {
		t.Error("provider without adapter accepted")
	}
	// Codex needs a login before it can be used.
	if _, err := resolveModel("", "openai-codex/gpt-5"); err == nil {
		t.Error("codex resolved without credentials")
	}
}

func TestDefaultModelResolves(t *testing.T) {
	agentDir(t, "")
	got, err := resolveModel("", DefaultModel)
	if err != nil || got.Model.Ref() != DefaultModel {
		t.Fatalf("got %+v err %v", got.Model, err)
	}
}
