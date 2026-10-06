package modelsfile

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestModel(t *testing.T) {
	f, err := Read(write(t, `{"providers":{"ollama":{"models":[
		{"id":"plain"},
		{"id":"big","contextWindow":131072,"maxTokens":8192,"cost":{"input":1.5,"output":6,"cacheRead":0.15,"cacheWrite":2}}
	]}}}`))
	if err != nil {
		t.Fatal(err)
	}

	got, ok := f.Model("ollama", "big")
	if !ok || got.ContextWindow != 131072 || got.MaxTokens != 8192 {
		t.Fatalf("big = %+v, %v", got, ok)
	}
	if got.Cost != (Cost{Input: 1.5, Output: 6, CacheRead: 0.15, CacheWrite: 2}) {
		t.Errorf("cost = %+v", got.Cost)
	}

	plain, ok := f.Model("ollama", "plain")
	if !ok || plain.ContextWindow != 0 || plain.Cost.Input != 0 {
		t.Errorf("plain = %+v, %v", plain, ok)
	}

	if _, ok := f.Model("ollama", "missing"); ok {
		t.Error("an unlisted model must not be found")
	}
	if _, ok := f.Model("openai-codex", "big"); ok {
		t.Error("a model listed under another provider must not be found")
	}
	if got := f.Models("ollama"); !reflect.DeepEqual(got, []string{"plain", "big"}) {
		t.Errorf("Models = %v, want file order", got)
	}
}

func TestProviderSettings(t *testing.T) {
	f, err := Read(write(t, `{"providers":{
		"ollama":{"baseUrl":"http://box:11434/v1","apiKey":"k","models":[{"id":"shared"}]},
		"openai":{"models":[{"id":"shared"}]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if f.BaseURL("ollama") != "http://box:11434/v1" || f.APIKey("ollama") != "k" {
		t.Errorf("ollama = %q, %q", f.BaseURL("ollama"), f.APIKey("ollama"))
	}
	if got := f.ProvidersOf("shared"); !reflect.DeepEqual(got, []string{"ollama", "openai"}) {
		t.Errorf("ProvidersOf = %v", got)
	}
	if got := f.ProvidersOf("none"); len(got) != 0 {
		t.Errorf("ProvidersOf(none) = %v", got)
	}
}

func TestMissingAndMalformed(t *testing.T) {
	f, err := Read(t.TempDir())
	if err != nil || len(f.Providers) != 0 {
		t.Errorf("no file: %+v, %v; want empty and no error", f, err)
	}
	if _, err := Read(write(t, `{nope`)); err == nil {
		t.Error("malformed file must be an error")
	}
}
