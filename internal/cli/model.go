package cli

import (
	"context"
	"time"

	"github.com/phucvinh57/pi-go/internal/ai"
	"github.com/phucvinh57/pi-go/internal/auth"
)

// resolvedModel is a model ready to call: where it lives, the adapter that
// speaks to it, and the credentials to use.
type resolvedModel struct {
	Model    ai.Model
	Provider ai.Provider
	Options  ai.Options
	// Subscription is true when the model is paid by a subscription login, so
	// Model.Cost is informational and not what the user is billed.
	Subscription bool
}

// windowFinder asks a server for a model's context window. It is a variable so
// tests do not reach the network.
var windowFinder = ai.ContextWindow

// windowLookupTimeout bounds asking an Ollama server for a context window; the
// answer only improves the footer, so a slow server must not delay a prompt.
const windowLookupTimeout = 2 * time.Second

// resolveModel turns a --model value ("provider/id", or a bare ID listed in
// models.json) plus an optional --provider into a callable model. cli is the
// place that joins auth and ai, because feature packages do not import each
// other.
func resolveModel(provider, ref string) (resolvedModel, error) {
	provider, err := auth.ResolveProvider(provider, ref)
	if err != nil {
		return resolvedModel{}, err
	}
	id := ref
	if prefix, rest := ai.SplitRef(ref); prefix == provider {
		id = rest
	}

	model, err := ai.NewModel(provider, id, auth.BaseURL(provider))
	if err != nil {
		return resolvedModel{}, err
	}
	p, err := ai.ProviderFor(model)
	if err != nil {
		return resolvedModel{}, err
	}
	cred, err := auth.ResolveAPIKey(provider)
	if err != nil {
		return resolvedModel{}, err
	}
	applyConfigured(&model)
	return resolvedModel{
		Model:        model,
		Provider:     p,
		Options:      ai.Options{APIKey: cred.Key},
		Subscription: cred.OAuth,
	}, nil
}

// applyConfigured fills in what models.json says about the model, which wins
// over the built-in table. When the context window is still unknown it asks the
// server, if the API has a way to; a failure just leaves it unknown.
func applyConfigured(m *ai.Model) {
	if e, ok := auth.ConfiguredModel(m.Provider, m.ID); ok {
		if e.ContextWindow > 0 {
			m.ContextWindow = e.ContextWindow
		}
		if e.MaxTokens > 0 {
			m.MaxTokens = e.MaxTokens
		}
		m.Cost = ai.Rates{
			Input: e.Cost.Input, Output: e.Cost.Output,
			CacheRead: e.Cost.CacheRead, CacheWrite: e.Cost.CacheWrite,
		}
	}
	if m.ContextWindow == 0 {
		ctx, cancel := context.WithTimeout(context.Background(), windowLookupTimeout)
		defer cancel()
		if n, err := windowFinder(ctx, *m); err == nil {
			m.ContextWindow = n
		}
	}
}
