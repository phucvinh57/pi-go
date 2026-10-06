// Package auth resolves provider credentials from auth.json, environment
// variables and models.json. Only the providers listed in providers are
// approved for now.
package auth

import (
	"fmt"
	"sort"
	"strings"

	"github.com/phucvinh57/pi-go/internal/modelsfile"
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
	// optional marks a provider that a feature can do without (laya: auto
	// routing falls back to heuristics). The pickers and `auth check` without
	// --provider leave it out; --provider still reaches it.
	optional bool
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
	// laya is the local classifier (laya-serve) that auto routing asks; it is
	// not a chat model, so it never appears in /model. It needs a key only
	// when the server was started with LAYA_API_KEY.
	"laya": {
		label:          "local classifier for auto routing",
		envVars:        []string{"LAYA_API_KEY"},
		defaultKey:     "laya",
		defaultBaseURL: "http://127.0.0.1:8000/v1",
		optional:       true,
	},
}

// LoginMethods describes how to log in to each provider of Primary, in the
// same order, e.g. "openai (API key)".
func LoginMethods() []string {
	ids := Primary()
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

// Primary returns the approved providers that are not optional, sorted: the
// ones a picker offers and `auth check` checks by default.
func Primary() []string {
	var ids []string
	for _, id := range Supported() {
		if !providers[id].optional {
			ids = append(ids, id)
		}
	}
	return ids
}

// IsSubscription reports whether provider is used through a subscription login
// rather than a metered API key, so the cost of its tokens is not a bill.
func IsSubscription(provider string) bool {
	return providers[provider].login == loginOAuth
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
func (s *Store) ResolveProvider(provider, model string) (string, error) {
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

	models, err := modelsfile.Read(s.dir)
	if err != nil {
		return "", err
	}
	matches := models.ProvidersOf(model)

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
