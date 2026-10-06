package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/phucvinh57/pi-go/internal/agent"
	"github.com/phucvinh57/pi-go/internal/ai"
	"github.com/phucvinh57/pi-go/internal/auth"
	sessionfile "github.com/phucvinh57/pi-go/internal/session"
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
	effort   string // reasoning effort; "" is the model's default
	agent    *agent.Agent

	// noSession turns off saving the conversation (--no-session).
	noSession bool
	// saved is where the conversation is being written; nil until the first
	// prompt, or when saving is off.
	saved *sessionfile.Writer
	// saveErrShown is set once a failure to save has been reported, so the
	// user hears about it once and not after every prompt.
	saveErrShown bool

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
		rm, err := resolveModel(s.provider, s.model)
		if err != nil {
			return "", err
		}
		rm.Options.Reasoning = s.effort
		var rec agent.Recorder
		if !s.noSession {
			s.saved = newWriter()
			s.saved.ModelChange(rm.Model.Provider, rm.Model.ID)
			rec = s.saved
		}
		built, err := buildAgent(rm, rec)
		if err != nil {
			return "", err
		}
		s.agent = built
	}
	var onEvent func(agent.Event)
	if emit != nil {
		onEvent = func(ev agent.Event) {
			if out, ok := toTUIEvent(ev); ok {
				emit(out)
			}
		}
	}
	reply, err := s.agent.PromptWith(ctx, prompt, onEvent)
	if emit != nil {
		if warning := s.takeSaveError(); warning != nil {
			emit(tui.Event{Kind: tui.EventWarning, Text: warning.Error()})
		}
	}
	if err != nil {
		return "", err
	}
	return reply.Text(), nil
}

// newWriter starts saving a session for the current directory.
func newWriter() *sessionfile.Writer {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return sessionfile.NewWriter(auth.AgentDir(), cwd, nil)
}

// SaveError returns why the conversation could not be saved, once; after that
// it returns nil. Print mode calls it after the answer, as it has no screen to
// show a warning on.
func (s *session) SaveError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.takeSaveError()
}

// takeSaveError is SaveError for callers that hold s.mu.
func (s *session) takeSaveError() error {
	if s.saved == nil || s.saveErrShown {
		return nil
	}
	err := s.saved.Err()
	s.saveErrShown = err != nil
	return err
}

// toTUIEvent adapts an agent event to the one the TUI shows; the two packages
// do not import each other. The bool is false for events the TUI has no use
// for.
func toTUIEvent(ev agent.Event) (tui.Event, bool) {
	out := tui.Event{Text: ev.Text, Tool: ev.Tool, Args: string(ev.Args), IsError: ev.IsError}
	switch ev.Type {
	case agent.EventThinking:
		out.Kind = tui.EventThinking
	case agent.EventToolStart:
		out.Kind = tui.EventToolStart
	case agent.EventToolEnd:
		out.Kind = tui.EventToolEnd
	case agent.EventStats:
		out.Kind = tui.EventStats
		out.Stats = toTUIStats(ev.Stats)
	case agent.EventTurnEnd:
		return tui.Event{}, false // its numbers arrive in EventStats
	default:
		out.Kind = tui.EventText
	}
	return out, true
}

func toTUIStats(s *agent.Stats) *tui.Stats {
	if s == nil {
		return nil
	}
	out := &tui.Stats{
		UserMessages:      s.UserMessages,
		AssistantMessages: s.AssistantMessages,
		ToolCalls:         s.ToolCalls,
		ToolResults:       s.ToolResults,
		Input:             s.Tokens.Input,
		Output:            s.Tokens.Output,
		CacheRead:         s.Tokens.CacheRead,
		CacheWrite:        s.Tokens.CacheWrite,
		Cost:              s.Tokens.Cost,
		CacheHit:          s.LastCacheHit,
		Subscription:      s.Subscription,
		ContextTokens:     s.Context.Tokens,
		ContextWindow:     s.Context.Window,
		ContextPercent:    s.Context.Percent,
	}
	for _, m := range s.ByModel {
		out.ByModel = append(out.ByModel, tui.ModelCost{Ref: m.Ref, Cost: m.Cost})
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
// provider. A provider the user is not logged in to is left out without a
// word. A provider that fails to list (server down) does not hide the others:
// the models found are returned together with an error that says what failed.
func (s *session) Choices(ctx context.Context) ([]string, error) {
	var (
		refs  []string
		warns []error
	)
	for _, provider := range ai.Providers() {
		cred, err := auth.ResolveAPIKey(provider)
		if errors.Is(err, auth.ErrNoCredentials) {
			continue
		}
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
	rm.Options.Reasoning = s.effort
	if s.agent != nil {
		s.agent.SetModel(rm.Provider, rm.Model, rm.Options, rm.Subscription)
		if s.saved != nil {
			s.saved.ModelChange(rm.Model.Provider, rm.Model.ID)
		}
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

// Effort returns the reasoning effort in use, or "" for the model's default.
func (s *session) Effort() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.effort
}

// Levels lists the efforts that can be chosen.
func (s *session) Levels() []string { return ai.EffortLevels }

// SetEffort sets the reasoning effort for the rest of the session, whichever
// model is active; "" goes back to the model's default.
func (s *session) SetEffort(level string) error {
	if !ai.ValidEffort(level) {
		return fmt.Errorf("unknown effort %q (choose %s)", level, strings.Join(ai.EffortLevels, ", "))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.effort = level
	if s.agent != nil {
		s.agent.SetReasoning(level)
	}
	return nil
}

// buildAgent makes the agent for a resolved model. rec, if not nil, is told
// about every message of the conversation.
func buildAgent(rm resolvedModel, rec agent.Recorder) (*agent.Agent, error) {
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
		Subscription: rm.Subscription,
		Recorder:     rec,
	}), nil
}
