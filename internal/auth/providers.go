// Package auth resolves provider credentials from auth.json, environment
// variables and models.json. Only the providers listed in providers are
// approved for now.
package auth

import (
	"fmt"
	"sort"
	"strings"
)

// loginMethod is how `auth login` obtains a credential for a provider.
type loginMethod int

const (
	loginAPIKey loginMethod = iota
	loginOAuth
)

func (m loginMethod) String() string {
	if m == loginOAuth {
		return "OAuth"
	}
	return "API key"
}

type providerSpec struct {
	login loginMethod
	// label is a human-readable note shown in login listings.
	label   string
	envVars []string
	// defaultKey is used when no credential is configured. Providers that
	// need no real key (a local server) set it; it is a placeholder, not a secret.
	defaultKey string
	// defaultBaseURL is used by readiness checks when models.json has no entry.
	defaultBaseURL string
}

var providers = map[string]providerSpec{
	"openai": {
		envVars: []string{"OPENAI_API_KEY"},
	},
	"ollama": {
		envVars:        []string{"OLLAMA_API_KEY"},
		defaultKey:     "ollama",
		defaultBaseURL: "http://localhost:11434/v1",
	},
	"openai-codex": {
		login: loginOAuth,
		label: "ChatGPT Plus/Pro",
	},
}

// LoginMethods describes how to log in to each approved provider, sorted by
// provider ID, e.g. "openai (API key)".
func LoginMethods() []string {
	ids := Supported()
	out := make([]string, len(ids))
	for i, id := range ids {
		spec := providers[id]
		desc := spec.login.String()
		if spec.label != "" {
			desc = spec.label + ", " + desc
		}
		out[i] = fmt.Sprintf("%s (%s)", id, desc)
	}
	return out
}

// Supported returns the approved provider IDs, sorted.
func Supported() []string {
	ids := make([]string, 0, len(providers))
	for id := range providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func lookup(provider string) (providerSpec, error) {
	spec, ok := providers[provider]
	if !ok {
		return providerSpec{}, fmt.Errorf("unsupported provider %q (supported: %s)", provider, strings.Join(Supported(), ", "))
	}
	return spec, nil
}

// ResolveProvider picks the provider for the --provider and --model flags.
// An explicit provider wins. Otherwise the model must be "provider/id" or an ID
// listed under a provider in models.json.
func ResolveProvider(provider, model string) (string, error) {
	if provider != "" {
		if _, err := lookup(provider); err != nil {
			return "", err
		}
		return provider, nil
	}

	if prefix, _, ok := strings.Cut(model, "/"); ok {
		if _, err := lookup(prefix); err != nil {
			return "", err
		}
		return prefix, nil
	}

	models, err := readModelsFile(AgentDir())
	if err != nil {
		return "", err
	}
	var matches []string
	for id, p := range models.Providers {
		for _, m := range p.Models {
			if m.ID == model {
				matches = append(matches, id)
				break
			}
		}
	}
	sort.Strings(matches)

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("cannot resolve a provider for model %q; use --provider or %q", model, "provider/id")
	case 1:
		if _, err := lookup(matches[0]); err != nil {
			return "", err
		}
		return matches[0], nil
	default:
		return "", fmt.Errorf("model %q is defined by several providers (%s); use --provider", model, strings.Join(matches, ", "))
	}
}
