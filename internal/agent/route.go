package agent

import (
	"context"

	"github.com/phucvinh57/pi-go/internal/ai"
)

// Routing is PI's virtual model: when Config.Router is set, the agent asks it
// which model and effort to use before every model call, instead of always
// using the one configured. The router sees why the call is made, so it can
// decide once per prompt and keep that choice for the calls that follow tool
// results.

// RouteReason says why a model call is being routed.
type RouteReason string

const (
	// RouteUser is the first call after the user's message.
	RouteUser RouteReason = "user"
	// RouteContinuation is any later call of the same prompt, after tool results.
	RouteContinuation RouteReason = "continuation"
)

// Route is the model one call goes to, and how.
type Route struct {
	Provider     ai.Provider
	Model        ai.Model
	Options      ai.Options
	Subscription bool
	// Note says why this route was picked, for the UI. Empty means there is
	// nothing worth showing; the EventRoute of a prompt is sent all the same,
	// so a UI knows which model answers.
	Note string
	// Warning is something the user should hear about that does not stop
	// the route, such as a provider that could not be listed. It is sent as
	// an EventWarning.
	Warning string
	// Charges are model calls the router made to decide, such as a
	// classifier. They are counted in the session's usage.
	Charges []Charge
}

// Charge is the usage of one call a router made.
type Charge struct {
	Ref   string // "provider/id"
	Usage ai.Usage
}

// RouteRequest is what a Router decides on.
type RouteRequest struct {
	Reason RouteReason
	// Current is where the call would go without the router: the previous
	// route, or the configured model. Its Note and Charges are empty.
	Current Route
	// Messages is the conversation, the user's latest message included. The
	// router must not change it.
	Messages      []ai.Message
	PlanMode      bool
	ContextTokens int // estimated size of the request
}

// Router picks the model of each call. It is called on the goroutine running
// the prompt.
type Router interface {
	Route(ctx context.Context, req RouteRequest) (Route, error)
}

// SetRouter sets or, with nil, removes the router, from the next model call
// on. Like SetModel, it must not be called while a Prompt is running.
func (a *Agent) SetRouter(r Router) { a.cfg.Router = r }

// route asks the router where the next call goes and switches the agent to it,
// so the reply, the stats and the context window all belong to the model that
// answers. A router that fails leaves the model as it was and the prompt goes
// on: the router is an optimisation, not a reason to fail.
func (a *Agent) route(ctx context.Context, reason RouteReason, emit func(Event)) {
	if a.cfg.Router == nil {
		return
	}
	current := Route{Provider: a.cfg.Provider, Model: a.cfg.Model, Options: a.cfg.Options, Subscription: a.cfg.Subscription}
	r, err := a.cfg.Router.Route(ctx, RouteRequest{
		Reason:        reason,
		Current:       current,
		Messages:      a.messages,
		PlanMode:      a.planMode,
		ContextTokens: a.contextTokens(),
	})
	for _, c := range r.Charges {
		a.tally.charge(c.Ref, c.Usage)
		if a.cfg.Recorder != nil {
			a.cfg.Recorder.Charge(c.Ref, c.Usage)
		}
	}
	if r.Warning != "" {
		emit(Event{Type: EventWarning, Text: r.Warning})
	}
	if err != nil {
		if ctx.Err() == nil {
			emit(Event{Type: EventWarning, Text: "routing failed, staying on " + a.cfg.Model.Ref() + ": " + err.Error()})
		}
		return
	}
	if r.Provider == nil {
		emit(Event{Type: EventWarning, Text: "routing picked no model, staying on " + a.cfg.Model.Ref()})
		return
	}
	a.cfg.Provider, a.cfg.Model, a.cfg.Options, a.cfg.Subscription = r.Provider, r.Model, r.Options, r.Subscription
	if reason == RouteUser || r.Note != "" {
		emit(Event{Type: EventRoute, Text: r.Note, Model: r.Model.Ref(), Effort: r.Options.Reasoning})
	}
}
