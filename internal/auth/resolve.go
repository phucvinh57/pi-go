package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/phucvinh57/pi-go/internal/modelsfile"
)

// ErrNoCredentials means no API key was found for the provider.
var ErrNoCredentials = errors.New("no credentials found")

// Credential is a resolved API key and where it came from. Source is safe to
// print; Key is not.
type Credential struct {
	Key    string
	Source string
	// OAuth marks a login to a subscription (ChatGPT) rather than a metered API
	// key, so what the model's tokens would cost is not what the user pays.
	OAuth bool
}

// ResolveAPIKey looks for a key in auth.json, then environment variables, then
// models.json, then the provider's placeholder default.
func (s *Store) ResolveAPIKey(provider string) (Credential, error) {
	spec, err := lookup(provider)
	if err != nil {
		return Credential{}, err
	}
	stored, err := readAuthFile(s.dir)
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
		return Credential{Key: c.Access, Source: "auth.json (oauth)", OAuth: true}, nil
	}

	if c, ok := stored[provider]; ok && c.Type == "api_key" && c.Key != "" {
		return Credential{Key: c.Key, Source: "auth.json"}, nil
	}

	for _, name := range spec.envVars {
		if key := os.Getenv(name); key != "" {
			return Credential{Key: key, Source: "env " + name}, nil
		}
	}

	models, err := modelsfile.Read(s.dir)
	if err != nil {
		return Credential{}, err
	}
	if key := models.APIKey(provider); key != "" {
		return Credential{Key: key, Source: "models.json"}, nil
	}

	if spec.defaultKey != "" {
		if stored[provider].Type == typeLoggedOut {
			return Credential{}, fmt.Errorf("%w for %s (logged out; run `pi-go auth login --provider %s`)",
				ErrNoCredentials, provider, provider)
		}
		return Credential{Key: spec.defaultKey, Source: "default"}, nil
	}

	return Credential{}, fmt.Errorf("%w for %s (set %s or add it to %s)",
		ErrNoCredentials, provider, strings.Join(spec.envVars, " or "), s.AuthPath())
}

// BaseURL returns the API base URL configured for provider in models.json, or
// the provider's default. It is empty when neither exists, which tells callers
// to use their own default.
func (s *Store) BaseURL(provider string) string {
	if models, err := modelsfile.Read(s.dir); err == nil {
		if url := models.BaseURL(provider); url != "" {
			return url
		}
	}
	return providers[provider].defaultBaseURL
}

// Status is the result of a readiness check. It never contains the key.
type Status struct {
	Provider string `json:"provider"`
	Ready    bool   `json:"ready"`
	Source   string `json:"source,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Check reports whether provider can be used. Local servers that need no real
// key (ollama) are additionally probed.
func (s *Store) Check(ctx context.Context, provider string) Status {
	status := Status{Provider: provider}

	cred, err := s.ResolveAPIKey(provider)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Source = cred.Source

	if probe, ok := probes[provider]; ok {
		if err := ping(ctx, provider, s.BaseURL(provider), probe); err != nil {
			status.Error = err.Error()
			return status
		}
	}

	status.Ready = true
	return status
}

// probes are the paths, under the server root, that answer when a local
// server is up. Ollama's root says "Ollama is running".
var probes = map[string]string{
	"ollama": "",
}

// ping asks a local server whether it is up. base is its API URL; the API
// lives under /v1, the probe at the root.
func ping(ctx context.Context, provider, base, probe string) error {
	root := strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1")

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, root+probe, nil)
	if err != nil {
		return fmt.Errorf("invalid %s base URL %q: %w", provider, base, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s server not reachable at %s: %w", provider, root, err)
	}
	resp.Body.Close()
	return nil
}
