package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// ErrNoCredentials means no API key was found for the provider.
var ErrNoCredentials = errors.New("no credentials found")

// Credential is a resolved API key and where it came from. Source is safe to
// print; Key is not.
type Credential struct {
	Key    string
	Source string
}

// ResolveAPIKey looks for a key in auth.json, then environment variables, then
// models.json, then the provider's placeholder default.
func ResolveAPIKey(provider string) (Credential, error) {
	spec, err := lookup(provider)
	if err != nil {
		return Credential{}, err
	}
	dir := AgentDir()

	stored, err := readAuthFile(dir)
	if err != nil {
		return Credential{}, err
	}

	if spec.login == loginOAuth {
		hint := fmt.Sprintf("run `pi-go auth login %s`", provider)
		c, ok := stored[provider]
		if !ok || c.Type != "oauth" || c.Access == "" {
			return Credential{}, fmt.Errorf("%w for %s (%s)", ErrNoCredentials, provider, hint)
		}
		if c.Expires <= time.Now().UnixMilli() {
			return Credential{}, fmt.Errorf("%s login has expired (%s)", provider, hint)
		}
		return Credential{Key: c.Access, Source: "auth.json (oauth)"}, nil
	}

	if c, ok := stored[provider]; ok && c.Type == "api_key" && c.Key != "" {
		return Credential{Key: c.Key, Source: "auth.json"}, nil
	}

	for _, name := range spec.envVars {
		if key := os.Getenv(name); key != "" {
			return Credential{Key: key, Source: "env " + name}, nil
		}
	}

	models, err := readModelsFile(dir)
	if err != nil {
		return Credential{}, err
	}
	if key := models.Providers[provider].APIKey; key != "" {
		return Credential{Key: key, Source: "models.json"}, nil
	}

	if spec.defaultKey != "" {
		return Credential{Key: spec.defaultKey, Source: "default"}, nil
	}

	return Credential{}, fmt.Errorf("%w for %s (set %s or add it to %s/auth.json)",
		ErrNoCredentials, provider, strings.Join(spec.envVars, " or "), dir)
}

// BaseURL returns the API base URL configured for provider in models.json, or
// the provider's default. It is empty when neither exists, which tells callers
// to use their own default.
func BaseURL(provider string) string {
	if models, err := readModelsFile(AgentDir()); err == nil {
		if url := models.Providers[provider].BaseURL; url != "" {
			return url
		}
	}
	return providers[provider].defaultBaseURL
}

// ConfiguredModels returns the model IDs models.json lists for provider, in
// file order. A missing file or provider yields none.
func ConfiguredModels(provider string) []string {
	models, err := readModelsFile(AgentDir())
	if err != nil {
		return nil
	}
	var ids []string
	for _, m := range models.Providers[provider].Models {
		ids = append(ids, m.ID)
	}
	return ids
}

// Status is the result of a readiness check. It never contains the key.
type Status struct {
	Provider string `json:"provider"`
	Ready    bool   `json:"ready"`
	Source   string `json:"source,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Check reports whether provider can be used. Providers that need no real key
// (ollama) are additionally probed for a reachable server.
func Check(ctx context.Context, provider string) Status {
	status := Status{Provider: provider}

	cred, err := ResolveAPIKey(provider)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Source = cred.Source

	if provider == "ollama" {
		if err := pingOllama(ctx); err != nil {
			status.Error = err.Error()
			return status
		}
	}

	status.Ready = true
	return status
}

func pingOllama(ctx context.Context) error {
	base := BaseURL("ollama")
	// The server root answers "Ollama is running"; the OpenAI-compatible API lives under /v1.
	root := strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1")

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, root, nil)
	if err != nil {
		return fmt.Errorf("invalid ollama base URL %q: %w", base, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("ollama server not reachable at %s: %w", root, err)
	}
	resp.Body.Close()
	return nil
}
