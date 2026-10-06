package route

import (
	"context"
	"strings"
	"unicode"
)

// Heuristic rates a prompt without calling anything: its length, the words it
// uses, the files tagged to it, plan mode and the size of the conversation. It
// is free, instant and private, but coarse: it cannot tell that a short request
// is hard. It never fails.
type Heuristic struct{}

// heuristicConfidence is how much the heuristics trust themselves: enough to
// switch models, too little to look sure.
const heuristicConfidence = 0.5

var (
	// hardWords suggest demanding work; easyWords suggest trivial work.
	hardWords = words("refactor redesign architecture architect design debug race deadlock concurrency concurrent optimize optimise performance migrate migration security vulnerability investigate rewrite")
	easyWords = words("rename typo format list show print hello hi hey thanks thank ok okay comment")
	// hardPhrases are matched on the lower-cased prompt with single spaces.
	hardPhrases = []string{"root cause", "why does", "why is", "step by step", "across the codebase", "end to end"}
	kindWords   = []struct {
		kind  string
		words map[string]bool
	}{
		{"plan", words("plan design architecture approach proposal")},
		{"review", words("review audit")},
		{"debug", words("bug fix error fails failing failure crash panic broken debug")},
		{"refactor", words("refactor restructure rename extract simplify cleanup")},
		{"explain", words("explain what why how where describe")},
	}
)

// Assess rates the prompt.
func (Heuristic) Assess(_ context.Context, req Request) (Assessment, error) {
	text := stripFiles(req.Prompt)
	lower := strings.Join(strings.Fields(strings.ToLower(text)), " ")
	tokens := strings.FieldsFunc(lower, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })

	hard, easy := false, false
	for _, t := range tokens {
		hard = hard || hardWords[t]
		easy = easy || easyWords[t]
	}
	for _, p := range hardPhrases {
		hard = hard || strings.Contains(lower, p)
	}

	demand := 1.0 // ordinary
	switch {
	case hard:
		demand += 1
	case easy:
		demand -= 0.75
	}
	switch n := len([]rune(lower)); {
	case n > 1500:
		demand += 0.5
	case n < 60:
		demand -= 0.25
	}
	demand += 0.25 * float64(min(countFiles(req.Prompt), 3))
	if req.PlanMode {
		demand += 0.5
	}
	if req.ContextTokens > 50_000 {
		demand += 0.25
	}

	return Assessment{Kind: kindOf(tokens, req.PlanMode, hard, easy), Demand: demand, Confidence: heuristicConfidence}, nil
}

// kindOf guesses the kind of work from the first kind whose words appear.
func kindOf(tokens []string, planMode, hard, easy bool) string {
	if planMode {
		return "plan"
	}
	if easy && !hard && len(tokens) <= 6 {
		return "chat"
	}
	for _, k := range kindWords {
		for _, t := range tokens {
			if k.words[t] {
				return k.kind
			}
		}
	}
	return "implement"
}

func words(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}
