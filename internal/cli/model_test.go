package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/phucvinh57/pi-go/internal/ai"
)

// agentDir returns an environment on a fresh agent directory, with models.json
// set to modelsJSON when that is not empty.
func agentDir(t *testing.T, modelsJSON string) environment {
	t.Helper()
	dir := t.TempDir()
	if modelsJSON != "" {
		if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(modelsJSON), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return environment{agentDir: dir, cwd: t.TempDir()}
}

func TestResolveModelOllama(t *testing.T) {
	env := agentDir(t, `{"providers":{"ollama":{"baseUrl":"http://gpu-box:11434/v1","models":[{"id":"qwen2.5:7b"}]}}}`)

	for _, ref := range []string{"ollama/qwen2.5:7b", "qwen2.5:7b"} {
		got, err := env.resolveModel("", ref)
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
	env := agentDir(t, "")
	got, err := env.resolveModel("ollama", "hf.co/org/model")
	if err != nil || got.Model.ID != "hf.co/org/model" {
		t.Fatalf("got %+v err %v", got.Model, err)
	}
	if got.Model.BaseURL != "http://localhost:11434/v1" {
		t.Errorf("base URL = %q", got.Model.BaseURL)
	}
}

func TestResolveModelErrors(t *testing.T) {
	env := agentDir(t, "")

	if _, err := env.resolveModel("", "nope/x"); err == nil {
		t.Error("unknown provider accepted")
	}
	if _, err := env.resolveModel("", "bare-model"); err == nil {
		t.Error("model with no provider accepted")
	}
	// Approved by auth, but no model adapter yet.
	if _, err := env.resolveModel("openai", "gpt-4o"); err == nil {
		t.Error("provider without adapter accepted")
	}
	// Codex needs a login before it can be used.
	if _, err := env.resolveModel("", "openai-codex/gpt-5"); err == nil {
		t.Error("codex resolved without credentials")
	}
}

func TestDefaultModelResolves(t *testing.T) {
	env := agentDir(t, "")
	got, err := env.resolveModel("", DefaultModel)
	if err != nil || got.Model.Ref() != DefaultModel {
		t.Fatalf("got %+v err %v", got.Model, err)
	}
}

// noWindowLookups keeps resolveModel from asking a server for the context
// window, and returns the models it was asked about.
func noWindowLookups(t *testing.T, answer int) *[]string {
	t.Helper()
	var asked []string
	old := windowFinder
	windowFinder = func(_ context.Context, m ai.Model) (int, error) {
		asked = append(asked, m.Ref())
		if answer == 0 {
			return 0, errors.New("unknown")
		}
		return answer, nil
	}
	t.Cleanup(func() { windowFinder = old })
	return &asked
}

func TestResolveModelUsesConfiguredLimitsAndCost(t *testing.T) {
	env := agentDir(t, `{"providers":{"ollama":{"models":[
		{"id":"big","contextWindow":131072,"maxTokens":8192,"cost":{"input":1,"output":4,"cacheRead":0.1,"cacheWrite":2}}
	]}}}`)
	asked := noWindowLookups(t, 4096)

	got, err := env.resolveModel("", "ollama/big")
	if err != nil {
		t.Fatal(err)
	}
	m := got.Model
	if m.ContextWindow != 131072 || m.MaxTokens != 8192 {
		t.Errorf("limits = %d/%d", m.ContextWindow, m.MaxTokens)
	}
	if m.Cost != (ai.Rates{Input: 1, Output: 4, CacheRead: 0.1, CacheWrite: 2}) {
		t.Errorf("cost = %+v", m.Cost)
	}
	if len(*asked) != 0 {
		t.Errorf("models.json knew the window, but the server was asked about %v", *asked)
	}
	if got.Subscription {
		t.Error("a local model is not a subscription")
	}
}

func TestResolveModelAsksServerForWindow(t *testing.T) {
	env := agentDir(t, "")
	asked := noWindowLookups(t, 32768)

	got, err := env.resolveModel("", "ollama/qwen")
	if err != nil {
		t.Fatal(err)
	}
	if got.Model.ContextWindow != 32768 || len(*asked) != 1 || (*asked)[0] != "ollama/qwen" {
		t.Errorf("window = %d, asked %v", got.Model.ContextWindow, *asked)
	}
}

func TestResolveModelWindowUnknownIsNotAnError(t *testing.T) {
	env := agentDir(t, "")
	noWindowLookups(t, 0)
	got, err := env.resolveModel("", "ollama/qwen")
	if err != nil || got.Model.ContextWindow != 0 {
		t.Fatalf("window = %d, err %v; want 0 and no error", got.Model.ContextWindow, err)
	}
}
