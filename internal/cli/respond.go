package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/phucvinh57/pi-go/internal/agent"
	"github.com/phucvinh57/pi-go/internal/ai"
	"github.com/phucvinh57/pi-go/internal/auth"
	"github.com/phucvinh57/pi-go/internal/settings"
	"github.com/phucvinh57/pi-go/internal/tools"
	"github.com/phucvinh57/pi-go/internal/tui"
)

// DefaultModel is used when neither --model nor settings.toml names a model.
const DefaultModel = "ollama/qwen2.5-coder:7b"

// session answers prompts with one agent and lets the user change its model.
// The agent is built on the first prompt, not up front, so a missing credential
// or model is reported when the user actually asks something, and
// `pi-go --help` or an `auth` subcommand never touches it. The agent lives as
// long as the session: in an interactive session each prompt continues the
// conversation, and switching models keeps it.
type session struct {
	mu       sync.Mutex
	provider string // from --provider; cleared once the model is picked
	model    string
	agent    *agent.Agent

	// settingsErr is why settings.toml could not be read while picking the
	// starting model. It is reported on the first prompt, not at startup.
	settingsErr error
}

// newSession starts on model if given (--model), else on the default model
// saved in settings.toml, else on DefaultModel.
func newSession(provider, model string) *session {
	s := &session{provider: provider, model: model}
	if model != "" {
		return s
	}
	saved, err := settings.DefaultModel()
	switch {
	case err != nil:
		s.settingsErr = err
		s.model = DefaultModel
	case saved != "":
		s.model = saved
	default:
		s.model = DefaultModel
	}
	return s
}

// Respond runs one prompt through the agent and returns the final reply.
func (s *session) Respond(ctx context.Context, prompt string) (string, error) {
	return s.respond(ctx, prompt, nil)
}

// Prompt runs one prompt and reports the reply and the tool calls to emit as
// they happen, for the interactive session.
func (s *session) Prompt(ctx context.Context, prompt string, emit func(tui.Event)) error {
	_, err := s.respond(ctx, prompt, emit)
	return err
}

func (s *session) respond(ctx context.Context, prompt string, emit func(tui.Event)) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.agent == nil {
		if s.settingsErr != nil {
			return "", s.settingsErr
		}
		built, err := buildAgent(s.provider, s.model)
		if err != nil {
			return "", err
		}
		s.agent = built
	}
	var onEvent func(agent.Event)
	if emit != nil {
		onEvent = func(ev agent.Event) { emit(toTUIEvent(ev)) }
	}
	reply, err := s.agent.PromptWith(ctx, prompt, onEvent)
	if err != nil {
		return "", err
	}
	return reply.Text(), nil
}

// toTUIEvent adapts an agent event to the one the TUI shows; the two packages
// do not import each other.
func toTUIEvent(ev agent.Event) tui.Event {
	out := tui.Event{Text: ev.Text, Tool: ev.Tool, Args: string(ev.Args), IsError: ev.IsError}
	switch ev.Type {
	case agent.EventThinking:
		out.Kind = tui.EventThinking
	case agent.EventToolStart:
		out.Kind = tui.EventToolStart
	case agent.EventToolEnd:
		out.Kind = tui.EventToolEnd
	case agent.EventTurnEnd:
		out.Kind = tui.EventUsage
		out.Tokens = ev.Usage.Input + ev.Usage.CacheRead + ev.Usage.Output
	default:
		out.Kind = tui.EventText
	}
	return out
}

// Current returns the active model as "provider/id". If the flags do not name
// a usable model yet, it returns them as given: the error is reported on the
// first prompt.
func (s *session) Current() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	provider, err := auth.ResolveProvider(s.provider, s.model)
	if err != nil {
		return s.model
	}
	id := s.model
	if prefix, rest := ai.SplitRef(s.model); prefix == provider {
		id = rest
	}
	return provider + "/" + id
}

// Choices lists the models that can be selected, as "provider/id", grouped by
// provider. A provider that cannot be listed (not logged in, server down) does
// not hide the others: the models found are returned together with an error
// that says what is missing.
func (s *session) Choices(ctx context.Context) ([]string, error) {
	var (
		refs  []string
		warns []error
	)
	for _, provider := range ai.Providers() {
		cred, err := auth.ResolveAPIKey(provider)
		if err != nil {
			warns = append(warns, fmt.Errorf("%s: %w", provider, err))
			continue
		}
		ids, err := ai.ListModels(ctx, provider, auth.BaseURL(provider), cred.Key)
		if err != nil {
			warns = append(warns, fmt.Errorf("%s: cannot list models: %w", provider, err))
		}
		ids = withConfigured(ids, auth.ConfiguredModels(provider))
		for _, id := range ids {
			refs = append(refs, provider+"/"+id)
		}
	}
	return refs, errors.Join(warns...)
}

// withConfigured adds the IDs from models.json that the provider did not list,
// and returns the result sorted.
func withConfigured(listed, configured []string) []string {
	seen := make(map[string]bool, len(listed))
	for _, id := range listed {
		seen[id] = true
	}
	for _, id := range configured {
		if !seen[id] {
			seen[id] = true
			listed = append(listed, id)
		}
	}
	sort.Strings(listed)
	return listed
}

// Select switches to ref ("provider/id", or a bare ID listed in models.json).
// It fails, leaving the current model in place, if the model cannot be
// resolved, for instance when the provider has no credentials.
func (s *session) Select(_ context.Context, ref string) error {
	rm, err := resolveModel("", ref)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.provider, s.model = rm.Model.Provider, rm.Model.Provider+"/"+rm.Model.ID
	if s.agent != nil {
		s.agent.SetModel(rm.Provider, rm.Model, rm.Options)
	}
	return nil
}

// SetDefault switches to ref, like Select, and saves it in settings.toml as the
// model to start with next time. Nothing is saved if ref cannot be used.
func (s *session) SetDefault(ctx context.Context, ref string) error {
	if err := s.Select(ctx, ref); err != nil {
		return err
	}
	return settings.SetDefaultModel(s.Current())
}

func buildAgent(provider, model string) (*agent.Agent, error) {
	rm, err := resolveModel(provider, model)
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("working directory: %w", err)
	}
	ts := tools.Core(cwd)
	return agent.New(agent.Config{
		Provider:     rm.Provider,
		Model:        rm.Model,
		Options:      rm.Options,
		SystemPrompt: agent.SystemPrompt(ts, cwd, time.Now()),
		Tools:        ts,
	}), nil
}
