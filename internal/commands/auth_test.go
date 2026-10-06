package commands

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pi-go/internal/auth"
	"pi-go/internal/config"
	"pi-go/internal/prompt"
)

// fakePrompter answers every Select with idx and every Input with secret, and
// records what was asked. A non-nil err fails every question instead.
type fakePrompter struct {
	idx     int
	secret  string
	err     error
	title   string
	options []string
	inputs  []string
}

func (f *fakePrompter) Select(_ context.Context, title string, options []string) (int, error) {
	f.title, f.options = title, options
	return f.idx, f.err
}

func (f *fakePrompter) Input(_ context.Context, title string, _ bool) (string, error) {
	f.inputs = append(f.inputs, title)
	return f.secret, f.err
}

// runAuth runs the auth command with p answering questions; a nil p means
// stdin is read as lines and nothing can be chosen.
func runAuth(t *testing.T, p *fakePrompter, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := newAuthCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	ctx := context.Background()
	if p != nil {
		ctx = prompt.With(ctx, p)
	}
	err := cmd.ExecuteContext(ctx)
	return out.String(), err
}

func TestLoginWithoutProviderAsksTheUser(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.AgentDirEnv, dir)

	providers := auth.Supported()
	var want int
	for i, id := range providers {
		if id == "openai" {
			want = i
		}
	}
	sel := &fakePrompter{idx: want, secret: "sk-test"}

	if _, err := runAuth(t, sel, "", "login"); err != nil {
		t.Fatal(err)
	}
	if len(sel.options) != len(providers) {
		t.Fatalf("offered %d options, want %d", len(sel.options), len(providers))
	}
	data, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "sk-test") {
		t.Fatalf("auth.json does not hold the key for the chosen provider: %s", data)
	}
}

func TestLoginWithProviderDoesNotAsk(t *testing.T) {
	t.Setenv(config.AgentDirEnv, t.TempDir())
	sel := &fakePrompter{secret: "sk-test"}

	if _, err := runAuth(t, sel, "", "login", "--provider", "openai"); err != nil {
		t.Fatal(err)
	}
	if sel.options != nil {
		t.Fatalf("asked the user although a provider was given: %v", sel.options)
	}
}

func TestLoginWithoutProviderNoTerminalFails(t *testing.T) {
	t.Setenv(config.AgentDirEnv, t.TempDir())

	// Without a prompter and a terminal nothing can be chosen.
	_, err := runAuth(t, nil, "", "login")
	if err == nil || !strings.Contains(err.Error(), "choose a provider") {
		t.Fatalf("err = %v, want a hint to choose a provider", err)
	}
}

func TestCheckAsksOnlyWithoutFlags(t *testing.T) {
	t.Setenv(config.AgentDirEnv, t.TempDir())

	sel := &fakePrompter{idx: 1} // first provider after "all providers"
	out, _ := runAuth(t, sel, "", "check")
	first := auth.Supported()[0]
	if len(sel.options) != len(auth.Supported())+1 {
		t.Fatalf("offered %d options, want providers plus \"all providers\"", len(sel.options))
	}
	if lines := strings.Count(out, "\n"); lines != 1 || !strings.HasPrefix(out, first+":") {
		t.Fatalf("checked more than the chosen provider %q:\n%s", first, out)
	}

	for _, args := range [][]string{{"check", "--provider", first}, {"check", "--json"}} {
		sel := &fakePrompter{}
		runAuth(t, sel, "", args...)
		if sel.options != nil {
			t.Fatalf("%v: asked the user", args)
		}
	}
}

func TestLoginCancelledWhileAskingForKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.AgentDirEnv, dir)

	p := &fakePrompter{err: prompt.ErrCancelled}
	_, err := runAuth(t, p, "", "login", "--provider", "openai")
	if !errors.Is(err, prompt.ErrCancelled) {
		t.Fatalf("err = %v, want it to wrap ErrCancelled", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "auth.json")); statErr == nil {
		t.Fatal("auth.json written although the user cancelled")
	}
}

func TestLoginCancelledWhileChoosing(t *testing.T) {
	t.Setenv(config.AgentDirEnv, t.TempDir())

	p := &fakePrompter{err: prompt.ErrCancelled}
	if _, err := runAuth(t, p, "", "login"); !errors.Is(err, prompt.ErrCancelled) {
		t.Fatalf("err = %v, want it to wrap ErrCancelled", err)
	}
	if len(p.inputs) != 0 {
		t.Fatalf("asked for input after the choice was cancelled: %v", p.inputs)
	}
}

func TestLoginReadsKeyFromPipedStdin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.AgentDirEnv, dir)

	if _, err := runAuth(t, nil, "sk-piped\n", "login", "--provider", "openai"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "auth.json"))
	if !strings.Contains(string(data), "sk-piped") {
		t.Fatalf("auth.json = %s", data)
	}

	if _, err := runAuth(t, nil, "", "login", "--provider", "openai"); err == nil || !strings.Contains(err.Error(), "no API key") {
		t.Fatalf("empty stdin: err = %v, want \"no API key provided\"", err)
	}
}

func TestLoginRejectsPositionalProvider(t *testing.T) {
	t.Setenv(config.AgentDirEnv, t.TempDir())

	if _, err := runAuth(t, &fakePrompter{}, "", "login", "openai"); err == nil {
		t.Fatal("a positional provider should be rejected; it is a flag now")
	}
}

func TestLogoutRemovesOnlyThatProvider(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.AgentDirEnv, dir)

	for _, id := range []string{"openai", "ollama"} {
		if _, err := runAuth(t, &fakePrompter{secret: "key-" + id}, "", "login", "--provider", id); err != nil {
			t.Fatal(err)
		}
	}

	out, err := runAuth(t, nil, "", "logout", "--provider", "openai")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Logged out of openai") {
		t.Fatalf("output = %q", out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "key-openai") || !strings.Contains(string(data), "key-ollama") {
		t.Fatalf("auth.json = %s", data)
	}

	out, err = runAuth(t, nil, "", "logout", "--provider", "openai")
	if err != nil || !strings.Contains(out, "Not logged in to openai") {
		t.Fatalf("second logout: %q, %v", out, err)
	}
}

func TestLogoutWithoutProviderAsksTheUser(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.AgentDirEnv, dir)
	if _, err := runAuth(t, &fakePrompter{secret: "sk-test"}, "", "login", "--provider", "openai"); err != nil {
		t.Fatal(err)
	}

	var want int
	for i, id := range auth.Supported() {
		if id == "openai" {
			want = i
		}
	}
	sel := &fakePrompter{idx: want}
	if _, err := runAuth(t, sel, "", "logout"); err != nil {
		t.Fatal(err)
	}
	if sel.title != "Log out of:" {
		t.Fatalf("title = %q", sel.title)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "auth.json"))
	if strings.Contains(string(data), "sk-test") {
		t.Fatalf("credential still stored: %s", data)
	}
}

func TestLogoutWithoutProviderNoTerminalFails(t *testing.T) {
	t.Setenv(config.AgentDirEnv, t.TempDir())

	_, err := runAuth(t, nil, "", "logout")
	if err == nil || !strings.Contains(err.Error(), "choose a provider") {
		t.Fatalf("err = %v, want a hint to choose a provider", err)
	}
}
