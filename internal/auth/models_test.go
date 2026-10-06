package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCredentialOAuthFlag(t *testing.T) {
	st, dir := tempStore(t)
	data, _ := json.Marshal(map[string]storedCredential{
		"openai-codex": {Type: "oauth", Access: "tok", Expires: time.Now().Add(time.Hour).UnixMilli()},
	})
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	cred, err := st.ResolveAPIKey("openai-codex")
	if err != nil || !cred.OAuth {
		t.Fatalf("codex: %+v, %v; want OAuth", cred, err)
	}

	local, err := st.ResolveAPIKey("ollama")
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
