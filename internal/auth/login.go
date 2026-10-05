package auth

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// LoginMethodOf reports how provider logs in: "api_key" or "oauth".
func LoginMethodOf(provider string) (string, error) {
	spec, err := lookup(provider)
	if err != nil {
		return "", err
	}
	if spec.login == loginOAuth {
		return "oauth", nil
	}
	return "api_key", nil
}

// SaveAPIKey stores key for an API-key provider in auth.json.
func SaveAPIKey(provider, key string) error {
	spec, err := lookup(provider)
	if err != nil {
		return err
	}
	if spec.login != loginAPIKey {
		return fmt.Errorf("%s logs in with OAuth, not an API key", provider)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("empty API key")
	}
	return saveCredential(AgentDir(), provider, storedCredential{Type: "api_key", Key: key})
}

// LoginOAuth runs the OAuth login for provider and stores the resulting
// tokens in auth.json. Prompts and progress go to out; in supplies a pasted
// redirect URL when the browser cannot reach the local callback server.
func LoginOAuth(ctx context.Context, provider string, in io.Reader, out io.Writer) error {
	spec, err := lookup(provider)
	if err != nil {
		return err
	}
	if spec.login != loginOAuth {
		return fmt.Errorf("%s logs in with an API key, not OAuth", provider)
	}
	switch provider {
	case "openai-codex":
		return loginCodex(ctx, in, out)
	default:
		return fmt.Errorf("no OAuth flow for %s", provider)
	}
}
