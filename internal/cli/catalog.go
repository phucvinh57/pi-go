package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/phucvinh57/pi-go/internal/ai"
	"github.com/phucvinh57/pi-go/internal/auth"
	"github.com/phucvinh57/pi-go/internal/modelsfile"
)

type modelListing struct {
	refs []string
	err  error
}

// listModels returns the selectable models for providers with credentials.
// Models configured in models.json remain selectable even when a provider's
// listing endpoint is unavailable.
func (e environment) listModels(ctx context.Context) modelListing {
	var (
		list  modelListing
		warns []error
	)
	store := e.auth()
	models, err := modelsfile.Read(e.agentDir)
	if err != nil {
		warns = append(warns, err)
	}
	for _, provider := range ai.Providers() {
		cred, err := store.ResolveAPIKey(provider)
		if errors.Is(err, auth.ErrNoCredentials) {
			continue
		}
		if err != nil {
			warns = append(warns, fmt.Errorf("%s: %w", provider, err))
			continue
		}

		baseURL := store.BaseURL(provider)
		ids, err := ai.ListModels(ctx, provider, baseURL, cred.Key)
		if err != nil {
			warns = append(warns, fmt.Errorf("%s: cannot list models: %w", provider, err))
		}
		for _, id := range withConfigured(ids, models.Models(provider)) {
			if _, err := ai.NewModel(provider, id, baseURL); err != nil {
				continue
			}
			list.refs = append(list.refs, provider+"/"+id)
		}
	}
	list.err = errors.Join(warns...)
	return list
}
