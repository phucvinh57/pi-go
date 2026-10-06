package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phucvinh57/pi-go/internal/config"
)

func agentDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(config.AgentDirEnv, dir)
	return dir
}

func TestDefaultModelMissingFile(t *testing.T) {
	agentDir(t)
	got, err := DefaultModel()
	if err != nil || got != "" {
		t.Fatalf("got %q, %v; want empty and no error", got, err)
	}
}

func TestSetDefaultModelRoundTrip(t *testing.T) {
	dir := agentDir(t)
	if err := SetDefaultModel("ollama/qwen2.5:7b"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.toml")); err != nil {
		t.Fatal(err)
	}
	got, err := DefaultModel()
	if err != nil || got != "ollama/qwen2.5:7b" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestSetDefaultModelCreatesAgentDir(t *testing.T) {
	dir := filepath.Join(agentDir(t), "nested", "agent")
	t.Setenv(config.AgentDirEnv, dir)
	if err := SetDefaultModel("ollama/a"); err != nil {
		t.Fatal(err)
	}
	if got, _ := DefaultModel(); got != "ollama/a" {
		t.Errorf("got %q", got)
	}
}

func TestSetDefaultModelKeepsOtherKeys(t *testing.T) {
	dir := agentDir(t)
	existing := "default_model = \"ollama/old\"\ntheme = \"dark\"\n\n[tui]\nquiet = true\n"
	if err := os.WriteFile(filepath.Join(dir, "settings.toml"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetDefaultModel("ollama/new"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "settings.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`default_model = 'ollama/new'`, `theme = 'dark'`, `[tui]`, `quiet = true`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("file lost %q:\n%s", want, data)
		}
	}
}

func TestMalformedFileIsAnErrorAndLeftAlone(t *testing.T) {
	dir := agentDir(t)
	path := filepath.Join(dir, "settings.toml")
	if err := os.WriteFile(path, []byte("default_model = "), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := DefaultModel(); err == nil {
		t.Error("malformed file accepted")
	}
	if err := SetDefaultModel("ollama/a"); err == nil {
		t.Error("write over a malformed file succeeded")
	}
	if data, _ := os.ReadFile(path); string(data) != "default_model = " {
		t.Errorf("file was modified: %q", data)
	}
}

func TestDefaultModelWrongType(t *testing.T) {
	dir := agentDir(t)
	if err := os.WriteFile(filepath.Join(dir, "settings.toml"), []byte("default_model = 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := DefaultModel(); err == nil {
		t.Error("non-string default_model accepted")
	}
}

func TestSetDefaultModelRejectsEmpty(t *testing.T) {
	agentDir(t)
	if err := SetDefaultModel("  "); err == nil {
		t.Error("empty ref accepted")
	}
}
