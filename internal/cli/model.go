package cli

import (
	"pi-go/internal/ai"
	"pi-go/internal/auth"
)

// resolvedModel is a model ready to call: where it lives, the adapter that
// speaks to it, and the credentials to use.
type resolvedModel struct {
	Model    ai.Model
	Provider ai.Provider
	Options  ai.Options
}

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
	return resolvedModel{Model: model, Provider: p, Options: ai.Options{APIKey: cred.Key}}, nil
}
