package cli

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/phucvinh57/pi-go/internal/agent"
	"github.com/phucvinh57/pi-go/internal/ai"
	"github.com/phucvinh57/pi-go/internal/auth"
	"github.com/phucvinh57/pi-go/internal/modelsfile"
	"github.com/phucvinh57/pi-go/internal/route"
	"github.com/phucvinh57/pi-go/internal/settings"
)

// AutoChoice is the model and the effort that auto routing picks per prompt.
const AutoChoice = "auto"

// LayaClassifier is the classifier auto routing is built for: Laya's English
// checkpoint on a local laya-serve, the one Laya recommends for score
// questions. It is off unless settings.toml [auto] classifier names it, since
// asking it sends every prompt to a local server the user must have chosen to
// run; until then, and whenever the server does not answer, the heuristics
// decide.
const LayaClassifier = "laya/english"

// noClassifier in [auto] classifier turns the classifier off, as leaving it
// out does.
const noClassifier = "none"

// How long the catalog keeps a listing before auto routing lists again.
const (
	// catalogTTL is for a listing every provider answered: models come and go
	// (`ollama pull`, `ollama rm`), but listing before each prompt is slow.
	catalogTTL = 5 * time.Minute
	// catalogRetry is for a listing a provider failed, so a server that was
	// still starting is picked up soon.
	catalogRetry = 30 * time.Second
)

// autoRouter is the agent.Router of a session whose model or effort is
// "auto". route makes the decision; this is where it becomes a callable model,
// because cli is the package that joins auth, models.json and ai.
//
// The session sets autoModel, autoEffort and effort before each prompt, and
// the agent calls Route on the prompt's goroutine, so they need no lock.
type autoRouter struct {
	env     environment
	catalog *catalog
	// autoModel and autoEffort say what is picked per prompt; effort is the
	// fixed effort used when autoEffort is off.
	autoModel, autoEffort bool
	effort                string
	// classifier is asked to rate prompts; nil leaves it to the heuristics.
	// It is a variable so tests need no laya-serve.
	classifier func(model ai.Model) (ai.Classifier, error)

	// windows and thinks keep what servers said about models, by ref, since
	// asking takes a round trip. Credentials and models.json are read again
	// on every route, so a new login or an edit applies at once.
	windows map[string]int
	thinks  map[string]bool
	// warned is the last warning given, so that a provider that stays down
	// is reported once, not before every prompt.
	warned string
}

func newAutoRouter(env environment, cat *catalog) *autoRouter {
	return &autoRouter{
		env:        env,
		catalog:    cat,
		classifier: ai.ClassifierFor,
		windows:    map[string]int{},
		thinks:     map[string]bool{},
	}
}

// Route decides the model and effort of a prompt on its first call, and keeps
// them for the calls after its tool results: switching mid-prompt would throw
// away the prompt cache for nothing.
func (r *autoRouter) Route(ctx context.Context, req agent.RouteRequest) (agent.Route, error) {
	if req.Reason != agent.RouteUser {
		return req.Current, nil
	}
	cfg, err := r.env.settings().Auto()
	if err != nil {
		return agent.Route{}, err
	}

	var (
		ladder  []route.Candidate
		warning string
	)
	if r.autoModel {
		var problem string
		if ladder, problem, err = r.ladder(ctx, cfg); err != nil {
			return agent.Route{}, err
		}
		warning = r.warnOnce(problem)
	}

	// A fixed model that cannot reason leaves no effort to pick.
	currentReasons := r.canReason(req.Current.Model)
	if !r.autoModel && !currentReasons {
		out := req.Current
		out.Options.Reasoning = ""
		return out, nil
	}

	current := req.Current.Model.Ref()
	if r.autoModel && !hasReply(req.Messages) {
		current = "" // no conversation yet, so no prompt cache to keep
	}
	primary, note := r.assessor(cfg)
	d, err := route.Router{Primary: primary, MinPromptChars: cfg.MinPromptChars}.Decide(ctx, route.Request{
		Prompt:        lastText(req.Messages, ai.RoleUser),
		LastReply:     lastText(req.Messages, ai.RoleAssistant),
		PlanMode:      req.PlanMode,
		ContextTokens: req.ContextTokens,
		Current:       current,
		CurrentEffort: req.Current.Options.Reasoning,
		NoEffort:      !currentReasons,
		Candidates:    ladder,
		AutoModel:     r.autoModel,
		AutoEffort:    r.autoEffort,
	})
	var charges []agent.Charge
	if d.UsageRef != "" {
		charges = []agent.Charge{{Ref: d.UsageRef, Usage: d.Usage}}
	}
	if err != nil {
		return agent.Route{Charges: charges, Warning: warning}, err
	}

	out := req.Current
	if r.autoModel && d.Ref != req.Current.Model.Ref() {
		rm, err := r.resolve(d.Ref)
		if err != nil {
			return agent.Route{Charges: charges, Warning: warning}, err
		}
		out = agent.Route{Provider: rm.Provider, Model: rm.Model, Options: rm.Options, Subscription: rm.Subscription}
	}
	out.Options.Reasoning = r.effort
	if r.autoEffort {
		out.Options.Reasoning = ""
		if r.canReason(out.Model) {
			out.Options.Reasoning = d.Effort
		}
	}
	out.Note, out.Charges, out.Warning = d.Note, charges, warning
	if out.Note != "" && note != "" {
		out.Note += " · " + note
	}
	return out, nil
}

// warnOnce returns problem the first time it comes up, and "" while it stays
// the same. A route without a problem resets it.
func (r *autoRouter) warnOnce(problem string) string {
	if problem == r.warned {
		return ""
	}
	r.warned = problem
	return problem
}

// ladder is the models auto may pick, weakest first, with a problem worth
// telling the user that did not stop it, such as a provider that could not
// be listed. It fails when there is no model to pick.
func (r *autoRouter) ladder(ctx context.Context, cfg settings.Auto) ([]route.Candidate, string, error) {
	list := r.catalog.get(ctx, r.env)
	// A provider that failed to list is likely down: its models from
	// models.json would only fail the prompt.
	cands := list.reachable()
	problems := []error{list.err}
	if len(cfg.Models) > 0 {
		listed := make(map[string]bool, len(cands))
		for _, c := range cands {
			listed[c.Ref] = true
		}
		for _, ref := range cfg.Models {
			if listed[ref] {
				continue
			}
			c, err := r.env.candidate(ref, list.down)
			if err != nil {
				problems = append(problems, fmt.Errorf("settings.toml [auto] models: %s: %w", ref, err))
				continue
			}
			listed[ref] = true
			cands = append(cands, c)
		}
	}
	ladder := route.Ladder(cands, cfg.Models)
	problem := errors.Join(problems...)
	switch {
	case len(ladder) > 0 && problem != nil:
		return ladder, "auto: " + problem.Error(), nil
	case len(ladder) > 0:
		return ladder, "", nil
	case problem != nil:
		return nil, "", fmt.Errorf("%w (%v)", route.ErrNoCandidates, problem)
	}
	return nil, "", route.ErrNoCandidates
}

// assessor returns the classifier to rate prompts with, or nil for the
// heuristics, and a note saying why there is none when that is worth telling.
// It looks again on every prompt, so a change to settings.toml, models.json or
// the login works at once. `pi-go auth logout --provider laya` turns it off.
func (r *autoRouter) assessor(cfg settings.Auto) (route.Assessor, string) {
	ref := cfg.Classifier
	if ref == "" || ref == noClassifier {
		return nil, ""
	}
	provider, id := ai.SplitRef(ref)
	store := r.env.auth()
	model, err := ai.NewClassifierModel(provider, id, store.BaseURL(provider))
	if err != nil {
		return nil, "classifier: " + err.Error()
	}
	cred, err := store.ResolveAPIKey(provider)
	if errors.Is(err, auth.ErrNoCredentials) {
		return nil, "" // turned off: the heuristics are the default, not a failure
	}
	if err != nil {
		return nil, "classifier: " + err.Error()
	}
	if file, err := modelsfile.Read(r.env.agentDir); err == nil {
		configure(&model, file) // a price from models.json, if any
	}
	c, err := r.classifier(model)
	if err != nil {
		return nil, "classifier: " + err.Error()
	}
	return route.Laya{
		Classifier: c,
		Model:      model,
		Options:    ai.Options{APIKey: cred.Key},
		Timeout:    time.Duration(cfg.TimeoutMS) * time.Millisecond,
	}, ""
}

// resolve makes ref callable. Only the context window is remembered between
// prompts: asking an Ollama server for it takes a round trip.
func (r *autoRouter) resolve(ref string) (resolvedModel, error) {
	return r.env.resolveWith("", ref, r.windows)
}

// canReason reports whether m takes a reasoning effort; see
// environment.canReason.
func (r *autoRouter) canReason(m ai.Model) bool { return r.env.canReason(m, r.thinks) }

// hasReply reports whether the conversation has an answer from a model yet.
func hasReply(msgs []ai.Message) bool {
	for _, m := range msgs {
		if m.Role == ai.RoleAssistant {
			return true
		}
	}
	return false
}

// lastText is the text of the latest message from role.
func lastText(msgs []ai.Message, role ai.Role) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == role {
			return msgs[i].Text()
		}
	}
	return ""
}

// catalog is the list of models a session can select, kept so that auto
// routing does not list every provider before each prompt. /model refreshes
// it, and it expires (catalogTTL, or catalogRetry when a provider failed).
// It has its own lock: listing may take seconds and must not wait for a
// running prompt.
type catalog struct {
	mu   sync.Mutex
	list listing
	at   time.Time
	// now is the clock; nil means time.Now.
	now func() time.Time
}

// listing is what listing the providers found.
type listing struct {
	cands []route.Candidate
	// down is the providers that could not list their models, and why.
	down map[string]error
	// err is every problem met, for the user; nil when there was none.
	err error
}

// reachable is the candidates whose provider listed its models.
func (l listing) reachable() []route.Candidate {
	if len(l.down) == 0 {
		return l.cands
	}
	var out []route.Candidate
	for _, c := range l.cands {
		if provider, _ := ai.SplitRef(c.Ref); l.down[provider] == nil {
			out = append(out, c)
		}
	}
	return out
}

func (c *catalog) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// get returns the cached listing while it is fresh, and lists the providers
// again otherwise. An empty listing is never kept.
func (c *catalog) get(ctx context.Context, env environment) listing {
	c.mu.Lock()
	defer c.mu.Unlock()
	ttl := catalogTTL
	if c.list.err != nil {
		ttl = catalogRetry
	}
	if len(c.list.cands) > 0 && c.clock().Sub(c.at) < ttl {
		return c.list
	}
	c.list, c.at = env.listModels(ctx), c.clock()
	return c.list
}

// refresh lists the providers again and keeps the result.
func (c *catalog) refresh(ctx context.Context, env environment) listing {
	list := env.listModels(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.list, c.at = list, c.clock()
	return list
}

// listModels lists the models that can be selected, with what is known about
// them, grouped by provider. A provider the user is not logged in to is left
// out without a word. A provider that fails to list (server down) does not
// hide the others: the models found are returned, and the failure is in down
// and err. Its models from models.json are still listed, for /model.
func (e environment) listModels(ctx context.Context) listing {
	var (
		list  = listing{down: map[string]error{}}
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
			list.down[provider] = err
			warns = append(warns, fmt.Errorf("%s: cannot list models: %w", provider, err))
		}
		for _, id := range withConfigured(ids, models.Models(provider)) {
			m, err := ai.NewModel(provider, id, baseURL)
			if err != nil {
				continue
			}
			configure(&m, models)
			list.cands = append(list.cands, newCandidate(m, cred))
		}
	}
	list.err = errors.Join(warns...)
	return list
}

// candidate makes a model no listing returned into a candidate, for [auto]
// models: /model can select a model its provider does not list (Codex leaves
// some out). It fails when ref is not "provider/id", or names a provider that
// could not list or that the user cannot use.
func (e environment) candidate(ref string, down map[string]error) (route.Candidate, error) {
	provider, id := ai.SplitRef(ref)
	if provider == "" {
		return route.Candidate{}, errors.New(`not a "provider/id"`)
	}
	if down[provider] != nil {
		return route.Candidate{}, fmt.Errorf("%s could not be listed", provider)
	}
	store := e.auth()
	cred, err := store.ResolveAPIKey(provider)
	if err != nil {
		return route.Candidate{}, err
	}
	m, err := ai.NewModel(provider, id, store.BaseURL(provider))
	if err != nil {
		return route.Candidate{}, err
	}
	if file, err := modelsfile.Read(e.agentDir); err == nil {
		configure(&m, file)
	}
	return newCandidate(m, cred), nil
}

func newCandidate(m ai.Model, cred auth.Credential) route.Candidate {
	return route.Candidate{
		Ref:           m.Ref(),
		ContextWindow: m.ContextWindow,
		Cost:          m.Cost,
		Subscription:  cred.OAuth,
		Local:         m.Provider == "ollama",
	}
}
