package agent

import (
	"sort"

	"github.com/phucvinh57/pi-go/internal/ai"
)

// Totals is what the model calls of a session used and cost.
type Totals struct {
	Input      int
	Output     int
	CacheRead  int
	CacheWrite int
	Cost       float64 // dollars
}

// Total is every token counted, cached or not.
func (t Totals) Total() int { return t.Input + t.Output + t.CacheRead + t.CacheWrite }

func (t *Totals) add(u ai.Usage) {
	t.Input += u.Input
	t.Output += u.Output
	t.CacheRead += u.CacheRead
	t.CacheWrite += u.CacheWrite
	t.Cost += u.Cost.Total
}

// ModelCost is the cost of the calls made to one model.
type ModelCost struct {
	Ref  string // "provider/id"
	Cost float64
}

// ContextUsage estimates how full the model's context is.
type ContextUsage struct {
	// Tokens is the estimated size of the next request.
	Tokens int
	// Window is the model's context window, 0 when unknown.
	Window int
	// Percent is Tokens as a share of Window; 0 when Window is unknown.
	Percent float64
}

// Stats describes a conversation: how big it is, and what its model calls have
// used so far.
type Stats struct {
	// Message counts are of the conversation as it stands, so a prompt that
	// failed and was rolled back is not in them.
	UserMessages      int
	AssistantMessages int
	ToolCalls         int
	ToolResults       int

	// Tokens and ByModel count every model call that was made, including the
	// ones of a prompt that failed and was rolled back: they were billed.
	Tokens  Totals
	ByModel []ModelCost // by cost, highest first; models that cost nothing are left out

	// LastCacheHit is the share, in percent, of the last call's prompt that was
	// read from the cache, or -1 when there is no call to tell from.
	LastCacheHit float64

	// Subscription is true when the current model is paid by a subscription.
	// SubscriptionCost is the part of Tokens.Cost that subscription models
	// used: what their tokens would cost, not what was billed. The rest was
	// billed. Switching models can mix both in one session.
	Subscription     bool
	SubscriptionCost float64

	Context ContextUsage
}

// tally is the running count behind Stats.
type tally struct {
	totals    Totals
	subCost   float64 // the part of totals.Cost paid by a subscription
	byModel   map[string]float64
	lastUsage ai.Usage
	hasLast   bool
}

// record adds the usage of one finished model call; subscription says the
// model is paid by a subscription.
func (t *tally) record(ref string, u ai.Usage, subscription bool) {
	t.totals.add(u)
	if subscription {
		t.subCost += u.Cost.Total
	}
	if t.byModel == nil {
		t.byModel = map[string]float64{}
	}
	t.byModel[ref] += u.Cost.Total
	if u.Input+u.CacheRead+u.CacheWrite > 0 {
		t.lastUsage, t.hasLast = u, true
	}
}

// Stats returns the current statistics. It reads the conversation, so it must
// not be called while a Prompt is running.
func (a *Agent) Stats() Stats {
	s := Stats{
		Tokens:           a.tally.totals,
		LastCacheHit:     -1,
		Subscription:     a.cfg.Subscription,
		SubscriptionCost: a.tally.subCost,
	}
	for _, m := range a.messages {
		switch m.Role {
		case ai.RoleUser:
			s.UserMessages++
		case ai.RoleAssistant:
			s.AssistantMessages++
			s.ToolCalls += len(m.ToolCalls())
		case ai.RoleToolResult:
			s.ToolResults++
		}
	}

	for ref, cost := range a.tally.byModel {
		if cost > 0 {
			s.ByModel = append(s.ByModel, ModelCost{Ref: ref, Cost: cost})
		}
	}
	sort.Slice(s.ByModel, func(i, j int) bool {
		if s.ByModel[i].Cost != s.ByModel[j].Cost {
			return s.ByModel[i].Cost > s.ByModel[j].Cost
		}
		return s.ByModel[i].Ref < s.ByModel[j].Ref
	})

	if u := a.tally.lastUsage; a.tally.hasLast {
		s.LastCacheHit = float64(u.CacheRead) / float64(u.Input+u.CacheRead+u.CacheWrite) * 100
	}

	s.Context.Window = a.cfg.Model.ContextWindow
	s.Context.Tokens = a.contextTokens()
	if s.Context.Window > 0 {
		s.Context.Percent = float64(s.Context.Tokens) / float64(s.Context.Window) * 100
	}
	return s
}

// contextTokens estimates the size of the next request the way PI does: the
// usage the provider reported for the last assistant message already counts
// everything up to it, so only the messages after it are estimated. With no
// reported usage at all, everything is estimated, system prompt included.
func (a *Agent) contextTokens() int {
	last := -1
	for i := len(a.messages) - 1; i >= 0; i-- {
		m := a.messages[i]
		if m.Role == ai.RoleAssistant && ai.ContextTokens(m.Usage) > 0 {
			last = i
			break
		}
	}
	if last < 0 {
		_, system := a.active()
		n := estimateChars(len(system))
		for _, m := range a.messages {
			n += estimateTokens(m)
		}
		return n
	}
	n := ai.ContextTokens(a.messages[last].Usage)
	for _, m := range a.messages[last+1:] {
		n += estimateTokens(m)
	}
	return n
}

// estimateTokens is the usual rough count of four characters per token, for
// text the provider has not counted yet.
func estimateTokens(m ai.Message) int {
	chars := 0
	for _, b := range m.Content {
		chars += len(b.Text) + len(b.Name) + len(b.Arguments)
	}
	return estimateChars(chars)
}

func estimateChars(chars int) int { return (chars + 3) / 4 }
