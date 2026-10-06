package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func agentDir(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	return NewStore(dir), dir
}

func TestDefaultModelMissingFile(t *testing.T) {
	st, _ := agentDir(t)
	got, err := st.DefaultModel()
	if err != nil || got != "" {
		t.Fatalf("got %q, %v; want empty and no error", got, err)
	}
}

func TestSetDefaultModelRoundTrip(t *testing.T) {
	st, dir := agentDir(t)
	if err := st.SetDefaultModel("ollama/qwen2.5:7b"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.toml")); err != nil {
		t.Fatal(err)
	}
	got, err := st.DefaultModel()
	if err != nil || got != "ollama/qwen2.5:7b" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestSetDefaultModelCreatesAgentDir(t *testing.T) {
	_, base := agentDir(t)
	st := NewStore(filepath.Join(base, "nested", "agent"))
	if err := st.SetDefaultModel("ollama/a"); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.DefaultModel(); got != "ollama/a" {
		t.Errorf("got %q", got)
	}
}

func TestSetDefaultModelKeepsOtherKeys(t *testing.T) {
	st, dir := agentDir(t)
	existing := "default_model = \"ollama/old\"\ntheme = \"dark\"\n\n[tui]\nquiet = true\n"
	if err := os.WriteFile(filepath.Join(dir, "settings.toml"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDefaultModel("ollama/new"); err != nil {
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
	st, dir := agentDir(t)
	path := filepath.Join(dir, "settings.toml")
	if err := os.WriteFile(path, []byte("default_model = "), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DefaultModel(); err == nil {
		t.Error("malformed file accepted")
	}
	if err := st.SetDefaultModel("ollama/a"); err == nil {
		t.Error("write over a malformed file succeeded")
	}
	if data, _ := os.ReadFile(path); string(data) != "default_model = " {
		t.Errorf("file was modified: %q", data)
	}
}

func TestDefaultModelWrongType(t *testing.T) {
	st, dir := agentDir(t)
	if err := os.WriteFile(filepath.Join(dir, "settings.toml"), []byte("default_model = 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DefaultModel(); err == nil {
		t.Error("non-string default_model accepted")
	}
}

func TestSetDefaultModelRejectsEmpty(t *testing.T) {
	st, _ := agentDir(t)
	if err := st.SetDefaultModel("  "); err == nil {
		t.Error("empty ref accepted")
	}
}
