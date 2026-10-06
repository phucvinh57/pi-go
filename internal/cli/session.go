package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/phucvinh57/pi-go/internal/agent"
	"github.com/phucvinh57/pi-go/internal/ai"
	"github.com/phucvinh57/pi-go/internal/tui"
)

// DefaultModel is used when neither --model nor settings.toml names a model.
const DefaultModel = "ollama/qwen2.5-coder:7b"

// session is what the interactive screen and print mode talk to: the model,
// reasoning effort and plan mode in force, and the conversation they apply to.
// It implements the tui.Models, Effort and Plan interfaces, so the screen
// sees none of auth, settings or the agent.
type session struct {
	env environment

	mu       sync.Mutex
	provider string // from --provider; cleared once the model is picked
	model    string
	effort   string // reasoning effort; "" is the model's default
	plan     bool   // plan mode: read-only tools, the model proposes a plan
	conv     conversation

	// autoModel and autoEffort are set when the model or the effort is
	// "auto": router picks them per prompt.
	autoModel, autoEffort bool
	router                *autoRouter // built on first use
	// catalog is the models that can be selected, as /model last listed them.
	catalog catalog

	// settingsErr is why settings.toml could not be read while picking the
	// starting model. It is reported on the first prompt, not at startup.
	settingsErr error
}

// newSession starts on model if given (--model), else on the default model
// saved in settings.toml, else on DefaultModel. Either may be "auto".
func newSession(env environment, provider, model string) *session {
	s := &session{env: env, provider: provider, model: model, conv: conversation{env: env}}
	if model == "" {
		saved, err := env.settings().DefaultModel()
		switch {
		case err != nil:
			s.settingsErr = err
			s.model = DefaultModel
		case saved != "":
			s.model = saved
		default:
			s.model = DefaultModel
		}
	}
	if s.model == AutoChoice {
		s.autoModel, s.provider, s.model = true, "", ""
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

	router := s.activeRouter()
	if !s.conv.started() {
		if s.settingsErr != nil {
			return "", s.settingsErr
		}
		rm, err := s.startingModel(ctx)
		if err != nil {
			return "", err
		}
		rm.Options.Reasoning = s.effort
		if err := s.conv.start(rm, s.plan, router); err != nil {
			return "", err
		}
	} else {
		s.conv.setRouter(router)
	}
	var onEvent func(agent.Event)
	if emit != nil {
		onEvent = func(ev agent.Event) {
			if out, ok := toTUIEvent(ev); ok {
				emit(out)
			}
		}
	}
	reply, err := s.conv.prompt(ctx, prompt, onEvent)
	if emit != nil {
		if warning := s.conv.takeSaveError(); warning != nil {
			emit(tui.Event{Kind: tui.EventWarning, Text: warning.Error()})
		}
	}
	if err != nil {
		return "", err
	}
	return reply.Text(), nil
}

// startingModel is the model the agent is built on. With an auto model it
// only stands in until the router picks one; the weakest model is as good as
// any for that, and confirms there is a model to pick.
func (s *session) startingModel(ctx context.Context) (resolvedModel, error) {
	if !s.autoModel {
		return s.env.resolveModel(s.provider, s.model)
	}
	cfg, err := s.env.settings().Auto()
	if err != nil {
		return resolvedModel{}, err
	}
	ladder, _, err := s.router.ladder(ctx, cfg)
	if err != nil {
		return resolvedModel{}, err
	}
	return s.router.resolve(ladder[0].Ref)
}

// activeRouter brings the router up to date with the session and returns it,
// or nil when neither the model nor the effort is auto. It is called with
// s.mu held.
func (s *session) activeRouter() agent.Router {
	if !s.autoModel && !s.autoEffort {
		return nil
	}
	if s.router == nil {
		s.router = newAutoRouter(s.env, &s.catalog)
	}
	s.router.autoModel, s.router.autoEffort, s.router.effort = s.autoModel, s.autoEffort, s.effort
	return s.router
}

// SaveError returns why the conversation could not be saved, once; after that
// it returns nil. Print mode calls it after the answer, as it has no screen to
// show a warning on.
func (s *session) SaveError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conv.takeSaveError()
}

// toTUIEvent adapts an agent event to the one the TUI shows; the two packages
// do not import each other. The bool is false for events the TUI has no use
// for.
func toTUIEvent(ev agent.Event) (tui.Event, bool) {
	out := tui.Event{Text: ev.Text, Tool: ev.Tool, Args: string(ev.Args), IsError: ev.IsError}
	switch ev.Type {
	case agent.EventText:
		out.Kind = tui.EventText
	case agent.EventThinking:
		out.Kind = tui.EventThinking
	case agent.EventToolStart:
		out.Kind = tui.EventToolStart
	case agent.EventToolEnd:
		out.Kind = tui.EventToolEnd
	case agent.EventStats:
		out.Kind = tui.EventStats
		out.Stats = toTUIStats(ev.Stats)
	case agent.EventRoute:
		out.Kind = tui.EventRoute
		out.Route = &tui.Route{Model: ev.Model, Effort: ev.Effort, Note: ev.Text}
	case agent.EventWarning:
		out.Kind = tui.EventWarning
	default:
		return tui.Event{}, false // EventTurnEnd: its numbers arrive in EventStats
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
		SubscriptionCost:  s.SubscriptionCost,
		ContextTokens:     s.Context.Tokens,
		ContextWindow:     s.Context.Window,
		ContextPercent:    s.Context.Percent,
	}
	for _, m := range s.ByModel {
		out.ByModel = append(out.ByModel, tui.ModelCost{Ref: m.Ref, Cost: m.Cost})
	}
	return out
}

// Current returns the active model as "provider/id", or "auto". If the flags
// do not name a usable model yet, it returns them as given: the error is
// reported on the first prompt.
func (s *session) Current() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.autoModel {
		return AutoChoice
	}
	provider, err := s.env.auth().ResolveProvider(s.provider, s.model)
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
// provider, after "auto". A provider the user is not logged in to is left out
// without a word. A provider that fails to list (server down) does not hide the
// others: the models found are returned together with an error that says what
// failed. The list is kept for auto routing to pick from.
func (s *session) Choices(ctx context.Context) ([]string, error) {
	list := s.catalog.refresh(ctx, s.env)
	if len(list.cands) == 0 {
		return nil, list.err
	}
	refs := []string{AutoChoice}
	for _, c := range list.cands {
		refs = append(refs, c.Ref)
	}
	return refs, list.err
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

// Select switches to ref ("provider/id", a bare ID listed in models.json, or
// "auto"). It fails, leaving the current model in place, if the model cannot be
// resolved, for instance when the provider has no credentials.
func (s *session) Select(_ context.Context, ref string) error {
	if ref == AutoChoice {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.autoModel = true
		return nil
	}
	rm, err := s.env.resolveModel("", ref)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.autoModel = false
	s.provider, s.model = rm.Model.Provider, rm.Model.Provider+"/"+rm.Model.ID
	rm.Options.Reasoning = s.effort
	s.conv.setModel(rm)
	return nil
}

// SetDefault switches to ref, like Select, and saves it in settings.toml as the
// model to start with next time. Nothing is saved if ref cannot be used.
func (s *session) SetDefault(ctx context.Context, ref string) error {
	if err := s.Select(ctx, ref); err != nil {
		return err
	}
	return s.env.settings().SetDefaultModel(s.Current())
}

// Effort returns the reasoning effort in use, "auto", or "" for the model's
// default.
func (s *session) Effort() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.autoEffort {
		return AutoChoice
	}
	return s.effort
}

// Levels lists the efforts that can be chosen.
func (s *session) Levels() []string { return append([]string{AutoChoice}, ai.EffortLevels...) }

// SetEffort sets the reasoning effort for the rest of the session, whichever
// model is active: a level, or "auto" to pick one per prompt. An unset session
// uses the model's default.
func (s *session) SetEffort(level string) error {
	if level != AutoChoice && (level == "" || !ai.ValidEffort(level)) {
		return fmt.Errorf("unknown effort %q (choose %s, %s)", level, AutoChoice, strings.Join(ai.EffortLevels, ", "))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if level == AutoChoice {
		s.autoEffort = true
		return nil
	}
	s.autoEffort = false
	s.effort = level
	s.conv.setReasoning(level)
	return nil
}

// PlanMode reports whether plan mode is on.
func (s *session) PlanMode() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.plan
}

// SetPlanMode turns plan mode on or off for the rest of the session, whichever
// model is active.
func (s *session) SetPlanMode(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plan = on
	s.conv.setPlanMode(on)
}
