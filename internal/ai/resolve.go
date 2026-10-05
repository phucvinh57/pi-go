package ai

import (
	"fmt"
	"sort"
	"strings"
)

// providerSpec is what the package knows about a provider ID.
type providerSpec struct {
	api            string
	defaultBaseURL string
}

var providerSpecs = map[string]providerSpec{
	"ollama":       {api: APICompletions, defaultBaseURL: "http://localhost:11434/v1"},
	"openai-codex": {api: APICodexResponses, defaultBaseURL: DefaultCodexBaseURL},
}

var apis = map[string]Provider{
	APICompletions:    wireProvider{completions{}},
	APICodexResponses: wireProvider{codex{}},
}

// Providers returns the provider IDs this package can talk to, sorted.
func Providers() []string {
	ids := make([]string, 0, len(providerSpecs))
	for id := range providerSpecs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// SplitRef splits "provider/id" at the first slash. A ref without a slash has
// no provider. Model IDs may contain colons and slashes ("qwen2.5:7b",
// "hf.co/org/model"), so only the first slash separates.
func SplitRef(ref string) (provider, id string) {
	if p, rest, ok := strings.Cut(ref, "/"); ok {
		return p, rest
	}
	return "", ref
}

// NewModel builds the Model for a provider and model ID. baseURL overrides the
// provider's default when non-empty.
func NewModel(provider, id, baseURL string) (Model, error) {
	spec, ok := providerSpecs[provider]
	if !ok {
		return Model{}, fmt.Errorf("no model adapter for provider %q (available: %s)", provider, strings.Join(Providers(), ", "))
	}
	if id == "" {
		return Model{}, fmt.Errorf("empty model ID for provider %q", provider)
	}
	if baseURL == "" {
		baseURL = spec.defaultBaseURL
	}
	return Model{Provider: provider, ID: id, API: spec.api, BaseURL: baseURL}, nil
}

// ProviderFor returns the Provider that speaks the model's API.
func ProviderFor(m Model) (Provider, error) {
	p, ok := apis[m.API]
	if !ok {
		return nil, fmt.Errorf("unknown API %q for model %s", m.API, m.Ref())
	}
	return p, nil
}
