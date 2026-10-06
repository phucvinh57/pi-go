package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeModelsJSON(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestConfiguredModel(t *testing.T) {
	dir := useTempAgentDir(t)
	writeModelsJSON(t, dir, `{"providers":{"ollama":{"models":[
		{"id":"plain"},
		{"id":"big","contextWindow":131072,"maxTokens":8192,"cost":{"input":1.5,"output":6,"cacheRead":0.15,"cacheWrite":2}}
	]}}}`)

	got, ok := ConfiguredModel("ollama", "big")
	if !ok || got.ContextWindow != 131072 || got.MaxTokens != 8192 {
		t.Fatalf("big = %+v, %v", got, ok)
	}
	if got.Cost.Input != 1.5 || got.Cost.Output != 6 || got.Cost.CacheRead != 0.15 || got.Cost.CacheWrite != 2 {
		t.Errorf("cost = %+v", got.Cost)
	}

	plain, ok := ConfiguredModel("ollama", "plain")
	if !ok || plain.ContextWindow != 0 || plain.Cost.Input != 0 {
		t.Errorf("plain = %+v, %v", plain, ok)
	}

	if _, ok := ConfiguredModel("ollama", "missing"); ok {
		t.Error("an unlisted model must not be found")
	}
	if _, ok := ConfiguredModel("openai-codex", "big"); ok {
		t.Error("a model listed under another provider must not be found")
	}
}

func TestConfiguredModelNoFile(t *testing.T) {
	useTempAgentDir(t)
	if _, ok := ConfiguredModel("ollama", "x"); ok {
		t.Error("no models.json: nothing configured")
	}
}

func TestCredentialOAuthFlag(t *testing.T) {
	dir := useTempAgentDir(t)
	data, _ := json.Marshal(map[string]storedCredential{
		"openai-codex": {Type: "oauth", Access: "tok", Expires: time.Now().Add(time.Hour).UnixMilli()},
	})
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	cred, err := ResolveAPIKey("openai-codex")
	if err != nil || !cred.OAuth {
		t.Fatalf("codex: %+v, %v; want OAuth", cred, err)
	}

	local, err := ResolveAPIKey("ollama")
	if err != nil || local.OAuth {
		t.Fatalf("ollama: %+v, %v; want not OAuth", local, err)
	}
}

func TestIsSubscription(t *testing.T) {
	for provider, want := range map[string]bool{"openai-codex": true, "ollama": false, "openai": false, "nope": false} {
		if got := IsSubscription(provider); got != want {
			t.Errorf("IsSubscription(%q) = %v, want %v", provider, got, want)
		}
	}
}
