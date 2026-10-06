package ai

import (
	"bytes"
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

// Limits of the models the ChatGPT backend serves, mirroring PI's catalog.
// They are not prices: that backend is paid by a subscription, so cost stays
// zero unless models.json sets it.
const (
	codexContextWindow      = 272_000
	codexSparkContextWindow = 128_000
	codexMaxTokens          = 128_000
)

// codexLimits returns the context window and output limit of a Codex model.
// A model it does not know (a new release) gets the default window.
func codexLimits(id string) (contextWindow, maxTokens int) {
	if id == "gpt-5.3-codex-spark" {
		return codexSparkContextWindow, codexMaxTokens
	}
	return codexContextWindow, codexMaxTokens
}

// ContextWindow asks the server for the context window of an Ollama model. It
// reads `model_info["<arch>.context_length"]` from POST /api/show, which the
// OpenAI-compatible /v1 API does not expose. It returns 0 and an error when the
// server does not say, and callers treat that as "unknown".
func ContextWindow(ctx context.Context, m Model) (int, error) {
	if m.API != APICompletions {
		return 0, fmt.Errorf("provider %q does not report a context window", m.Provider)
	}
	// BaseURL is the OpenAI-compatible root ("http://host:11434/v1"); the native
	// API lives one level up.
	root := strings.TrimSuffix(strings.TrimRight(m.BaseURL, "/"), "/v1")
	body, err := json.Marshal(map[string]string{"model": m.ID})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, root+"/api/show", bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("invalid URL %q: %w", root, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := listClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return 0, &httpError{Status: resp.StatusCode}
	}

	var info struct {
		ModelInfo map[string]json.RawMessage `json:"model_info"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return 0, fmt.Errorf("decode model info: %w", err)
	}
	for key, raw := range info.ModelInfo {
		if !strings.HasSuffix(key, ".context_length") {
			continue
		}
		var n int
		if err := json.Unmarshal(raw, &n); err == nil && n > 0 {
			return n, nil
		}
	}
	return 0, fmt.Errorf("%s did not report a context length", m.Ref())
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
