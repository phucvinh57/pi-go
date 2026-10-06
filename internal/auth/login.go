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

// Logout removes the credential stored for provider in auth.json and reports
// whether anything changed. Credentials from environment variables or
// models.json are not touched. A provider that works without a real key
// (ollama) is marked as logged out, so it is no longer used until the next
// login.
func Logout(provider string) (bool, error) {
	spec, err := lookup(provider)
	if err != nil {
		return false, err
	}
	return removeCredential(AgentDir(), provider, spec.defaultKey != "")
}

// PasteFunc asks the user for a pasted redirect URL. It must return when ctx
// ends. io.EOF means no one can answer (stdin closed): the login then waits
// for the browser alone. Any other error aborts the login.
type PasteFunc func(ctx context.Context) (string, error)

// LoginOAuth runs the OAuth login for provider and stores the resulting
// tokens in auth.json. Progress goes to out; paste supplies a redirect URL
// when the browser cannot reach the local callback server.
func LoginOAuth(ctx context.Context, provider string, paste PasteFunc, out io.Writer) error {
	spec, err := lookup(provider)
	if err != nil {
		return err
	}
	if spec.login != loginOAuth {
		return fmt.Errorf("%s logs in with an API key, not OAuth", provider)
	}
	switch provider {
	case "openai-codex":
		return loginCodex(ctx, paste, out)
	default:
		return fmt.Errorf("no OAuth flow for %s", provider)
	}
}
