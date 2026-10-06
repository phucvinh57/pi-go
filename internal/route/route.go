// Package route is auto routing: for each prompt it decides which model
// answers and with how much reasoning. An Assessor rates the prompt (the Laya
// classifier on a local laya-serve, or heuristics when Laya is turned off or
// does not answer), and Router turns that rating into a rung of the candidate ladder and
// an effort level, through one small table kept in code where it can be read.
//
// The package knows models only as refs with a price and a context window. cli
// lists the candidates and makes the chosen one callable.
package route

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/phucvinh57/pi-go/internal/ai"
)

const (
	// DefaultMinPromptChars is the length under which a prompt ("ok", "go
	// on") keeps the model and effort in use.
	DefaultMinPromptChars = 12
	// DefaultTimeout bounds the classifier call; past it, the heuristics decide.
	DefaultTimeout = 3500 * time.Millisecond
)

// The decision table. Demand runs from 0 (trivial) to 3 (architectural).
const (
	// stickyBand is how far the demand may be from the current model's rung
	// before switching is worth losing the prompt cache.
	stickyBand = 0.75
	// minConfidence is the confidence under which the current model is kept.
	// Laya's English checkpoint reports 0.15 to 0.5 for plain prompts, and
	// under 0.1 when the prompt and the previous reply pull apart ("thanks"
	// after a large refactor).
	minConfidence = 0.15
	// planFloor keeps planning and reviews off the weakest models.
	planFloor = 1.5
)

// demandLevels name the demand scale, and are the levels Laya scores on.
var demandLevels = []string{"trivial", "ordinary", "demanding", "architectural"}

// ErrNoCandidates means auto has no model to pick from.
var ErrNoCandidates = errors.New("auto: no model is available; log in to a provider with `pi-go auth login`")

// Request is one prompt to route.
type Request struct {
	Prompt        string
	LastReply     string // the previous answer, for follow-ups like "do it"
	PlanMode      bool
	ContextTokens int // estimated size of the conversation
	// Current is the model in use ("provider/id") and CurrentEffort its effort;
	// "" before the first prompt, or for the model's default effort.
	Current       string
	CurrentEffort string
	// NoEffort says the current model cannot reason, so its empty
	// CurrentEffort is where it stands, not unknown.
	NoEffort bool
	// Candidates are the models auto may pick, weakest first (see Ladder).
	Candidates []Candidate
	// AutoModel and AutoEffort say what to decide; the other stays Current.
	AutoModel, AutoEffort bool
}

// Decision is where a prompt goes.
type Decision struct {
	Ref    string
	Effort string
	// Note says why, for the user; empty when nothing was decided (a short
	// follow-up stays where it is).
	Note string
	// Fallback is set when the heuristics decided instead of the classifier.
	Fallback bool
	// Usage is what the classifier call used, and UsageRef the classifier;
	// UsageRef is empty when no call was made.
	Usage    ai.Usage
	UsageRef string
}

// Assessment is how demanding a prompt is.
type Assessment struct {
	Kind       string  // plan, implement, debug, refactor, review, explain or chat
	Demand     float64 // 0 (trivial) to 3 (architectural)
	Confidence float64 // 0 to 1
	Usage      ai.Usage
	UsageRef   string // the classifier called; "" for none
}

// Assessor rates a prompt. Laya is a call to a server; Heuristic is in-process.
type Assessor interface {
	Assess(ctx context.Context, req Request) (Assessment, error)
}

// Router decides with Primary, and with the heuristics when Primary is nil or
// fails.
type Router struct {
	Primary Assessor
	// MinPromptChars is DefaultMinPromptChars when 0.
	MinPromptChars int
}

// Decide routes one prompt. It fails only when there is no model to pick or
// ctx is done.
func (r Router) Decide(ctx context.Context, req Request) (Decision, error) {
	ladder := req.Candidates
	if req.AutoModel {
		if len(ladder) == 0 {
			return Decision{}, ErrNoCandidates
		}
		ladder = fitting(ladder, req.ContextTokens)
		if len(ladder) == 0 {
			return Decision{}, fmt.Errorf("auto: no model has room for the %d tokens of this conversation", req.ContextTokens)
		}
	}
	current := indexOf(ladder, req.Current)

	minChars := r.MinPromptChars
	if minChars == 0 {
		minChars = DefaultMinPromptChars
	}
	known := (!req.AutoModel || current >= 0) && (!req.AutoEffort || req.CurrentEffort != "" || req.NoEffort)
	if known && len([]rune(strings.TrimSpace(stripFiles(req.Prompt)))) < minChars {
		return Decision{Ref: req.Current, Effort: req.CurrentEffort}, nil
	}

	var (
		a        Assessment
		err      error
		fallback string
	)
	if r.Primary != nil {
		a, err = r.Primary.Assess(ctx, req)
		if err != nil {
			if ctx.Err() != nil {
				return Decision{}, ctx.Err()
			}
			fallback = err.Error()
		}
	}
	if r.Primary == nil || err != nil {
		a, _ = Heuristic{}.Assess(ctx, req)
	}

	d := choose(req, ladder, current, a)
	if r.Primary == nil || err != nil {
		d.Fallback = true
		d.Note += " · heuristic"
		if fallback != "" {
			d.Note += " (" + shorten(fallback, 80) + ")"
		}
	}
	return d, nil
}

// choose applies the decision table to an assessment.
func choose(req Request, ladder []Candidate, current int, a Assessment) Decision {
	demand := math.Min(math.Max(a.Demand, 0), 3)
	if (a.Kind == "plan" || a.Kind == "review" || req.PlanMode) && demand < planFloor {
		demand = planFloor
	}

	d := Decision{Ref: req.Current, Effort: req.CurrentEffort, Usage: a.Usage, UsageRef: a.UsageRef}
	if req.AutoModel {
		i := rung(demand, len(ladder))
		switch {
		case current < 0:
		case a.Confidence < minConfidence:
			i = current
		case math.Abs(demand-rungDemand(current, len(ladder))) <= stickyBand:
			i = current
		}
		d.Ref = ladder[i].Ref
	}
	if req.AutoEffort {
		d.Effort = effortFor(demand)
	}

	kind := a.Kind
	if kind == "" {
		kind = "task"
	}
	d.Note = kind + ", " + demandLevels[int(math.Round(demand))]
	return d
}

// rung is the ladder index for a demand: trivial work goes to the weakest
// model, architectural work to the strongest, the rest in proportion.
func rung(demand float64, n int) int {
	if n <= 1 {
		return 0
	}
	return int(math.Round(demand / 3 * float64(n-1)))
}

// rungDemand is the demand a rung stands for, the inverse of rung.
func rungDemand(i, n int) float64 {
	if n <= 1 {
		return 0 // the only model: every demand ends there anyway
	}
	return float64(i) * 3 / float64(n-1)
}

// effortFor maps demand to a reasoning effort. It never picks "max": that is
// for the user to ask for.
func effortFor(demand float64) string {
	switch {
	case demand < 0.75:
		return "low"
	case demand < 1.5:
		return "medium"
	case demand < 2.25:
		return "high"
	}
	return "xhigh"
}

func indexOf(ladder []Candidate, ref string) int {
	for i, c := range ladder {
		if c.Ref == ref {
			return i
		}
	}
	return -1
}

// shorten cuts s to n runes, on one line.
func shorten(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
