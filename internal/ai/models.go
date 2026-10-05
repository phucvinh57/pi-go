package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// codexModels are the models the ChatGPT backend serves. It has no listing
// endpoint, so the catalog is fixed, mirroring PI's. Which models work depends
// on the ChatGPT plan, and nothing here can tell. gpt-5.3-codex-spark is left
// out because the backend refuses it for some accounts; `/model
// openai-codex/gpt-5.3-codex-spark` still selects it.
var codexModels = []string{
	"gpt-5.5",
	"gpt-5.6-luna",
	"gpt-5.6-sol",
	"gpt-5.6-terra",
	"gpt-6-astra",
	"gpt-6-luna",
	"gpt-6-sol",
}

// listClient bounds a listing: a hung server must not freeze the picker.
var listClient = &http.Client{Timeout: 5 * time.Second}

// ListModels returns the model IDs provider can serve, sorted. baseURL
// overrides the provider's default when non-empty; apiKey is sent when the
// provider needs one to list.
func ListModels(ctx context.Context, provider, baseURL, apiKey string) ([]string, error) {
	spec, ok := providerSpecs[provider]
	if !ok {
		return nil, fmt.Errorf("no model adapter for provider %q (available: %s)", provider, strings.Join(Providers(), ", "))
	}
	switch spec.api {
	case APICompletions:
		if baseURL == "" {
			baseURL = spec.defaultBaseURL
		}
		return listOpenAIModels(ctx, baseURL, apiKey)
	case APICodexResponses:
		return append([]string(nil), codexModels...), nil
	}
	return nil, fmt.Errorf("provider %q cannot list models", provider)
}

// listOpenAIModels reads GET {baseURL}/models, the OpenAI-compatible listing
// that Ollama serves too.
func listOpenAIModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	url := strings.TrimRight(baseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid URL %q: %w", url, err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := listClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, &httpError{Status: resp.StatusCode, Body: strings.TrimSpace(string(snippet))}
	}

	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode model list from %s: %w", url, err)
	}
	ids := make([]string, 0, len(body.Data))
	for _, m := range body.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}
