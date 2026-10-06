// Package modelsfile reads <agent dir>/models.json, PI's file for naming the
// models a provider offers and how to reach it: a base URL, an API key and a
// list of models with their limits and prices. It only reads; pi-go never
// writes the file.
//
// The file is read on every call to Read, so an edit made while a session is
// open applies to the next lookup.
package modelsfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// FileName is the name of the file inside the agent directory.
const FileName = "models.json"

// File is the parsed models.json. The zero value is an empty file.
type File struct {
	Providers map[string]provider `json:"providers"`
}

type provider struct {
	BaseURL string  `json:"baseUrl"`
	APIKey  string  `json:"apiKey"`
	Models  []model `json:"models"`
}

type model struct {
	ID            string `json:"id"`
	ContextWindow int    `json:"contextWindow"`
	MaxTokens     int    `json:"maxTokens"`
	Cost          *Cost  `json:"cost"`
	Reasoning     *bool  `json:"reasoning"`
}

// Cost is dollars per million tokens, as in PI's models.json.
type Cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// Entry is what the file says about one model, beyond its ID. Zero fields mean
// the file did not say.
type Entry struct {
	ContextWindow int
	MaxTokens     int
	Cost          Cost
	// Reasoning says whether the model takes a reasoning effort; nil when
	// the file does not say.
	Reasoning *bool
}

// Read parses models.json in dir. A missing file is an empty File, not an
// error; a malformed one is.
func Read(dir string) (File, error) {
	path := filepath.Join(dir, FileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return File{}, nil
	}
	if err != nil {
		return File{}, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return File{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return f, nil
}

// APIKey returns the key the file sets for provider, or "".
func (f File) APIKey(provider string) string { return f.Providers[provider].APIKey }

// BaseURL returns the base URL the file sets for provider, or "".
func (f File) BaseURL(provider string) string { return f.Providers[provider].BaseURL }

// Models returns the model IDs the file lists for provider, in file order.
func (f File) Models(provider string) []string {
	var ids []string
	for _, m := range f.Providers[provider].Models {
		ids = append(ids, m.ID)
	}
	return ids
}

// Model returns the entry for provider's model id. The bool is false when the
// file does not list it.
func (f File) Model(provider, id string) (Entry, bool) {
	for _, m := range f.Providers[provider].Models {
		if m.ID != id {
			continue
		}
		e := Entry{ContextWindow: m.ContextWindow, MaxTokens: m.MaxTokens, Reasoning: m.Reasoning}
		if m.Cost != nil {
			e.Cost = *m.Cost
		}
		return e, true
	}
	return Entry{}, false
}

// ProvidersOf returns the providers that list a model with this ID, sorted.
func (f File) ProvidersOf(id string) []string {
	var out []string
	for name, p := range f.Providers {
		for _, m := range p.Models {
			if m.ID == id {
				out = append(out, name)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}
