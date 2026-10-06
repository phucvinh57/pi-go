package route

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/phucvinh57/pi-go/internal/ai"
)

// Candidate is a model auto may pick.
type Candidate struct {
	Ref           string // "provider/id"
	ContextWindow int    // 0 when unknown
	Cost          ai.Rates
	// Subscription is set when a subscription login pays for the model, so
	// its price is not what decides.
	Subscription bool
	// Local is set for a model on the user's own server (Ollama): free, and
	// usually the weakest.
	Local bool
}

// notChat marks model IDs that cannot hold a coding conversation, which some
// providers list next to their chat models.
var notChat = []string{"embed", "tts", "whisper", "dall-e", "moderation", "transcribe", "realtime", "image", "audio", "rerank"}

// Ladder orders the candidates weakest first, the scale Router climbs as a
// prompt gets more demanding.
//
// When order is set (the [auto] models setting), it is the ladder: only those
// candidates, in that order. Otherwise strength is guessed from what is known:
// local models first, then remote models with no price, then priced ones from
// cheapest output to dearest, then subscription models, with ties broken by
// size and version (see weaker); models that cannot chat are left out.
func Ladder(cands []Candidate, order []string) []Candidate {
	if len(order) > 0 {
		byRef := make(map[string]Candidate, len(cands))
		for _, c := range cands {
			byRef[c.Ref] = c
		}
		var out []Candidate
		seen := map[string]bool{}
		for _, ref := range order {
			if c, ok := byRef[ref]; ok && !seen[ref] {
				seen[ref] = true
				out = append(out, c)
			}
		}
		return out
	}

	out := make([]Candidate, 0, len(cands))
	for _, c := range cands {
		if chatModel(c.Ref) {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ta, tb := tier(a), tier(b); ta != tb {
			return ta < tb
		}
		if a.Cost.Output != b.Cost.Output {
			return a.Cost.Output < b.Cost.Output
		}
		if a.Cost.Input != b.Cost.Input {
			return a.Cost.Input < b.Cost.Input
		}
		return weaker(a.Ref, b.Ref)
	})
	return out
}

// weaker guesses, between two models of the same tier and price, which is the
// weaker: the smaller by the parameter count in its ID ("llama3.2:1b" before
// "deepseek-r1:70b"), then, in one family, the older version ("gpt-5.5"
// before "gpt-6-sol"). Models it cannot tell apart keep the order of their
// refs; [auto] models orders them for sure.
func weaker(a, b string) bool {
	if sa, sb := paramCount(a), paramCount(b); sa != sb {
		return sa < sb // an unknown size (0) counts as small
	}
	fa, va := version(a)
	fb, vb := version(b)
	if fa == fb {
		if c := slices.Compare(va, vb); c != 0 {
			return c < 0
		}
	}
	return a < b
}

// sizeTag is a parameter count in a model ID, as Ollama tags them: "7b",
// "1.5b", "270m", "8x7b".
var sizeTag = regexp.MustCompile(`(?i)(?:^|[^a-z0-9.])(?:(\d+)x)?(\d+(?:\.\d+)?)([bm])(?:$|[^a-z0-9])`)

// paramCount is the model's size in billions of parameters, 0 when its ID
// does not say.
func paramCount(ref string) float64 {
	m := sizeTag.FindStringSubmatch(ref)
	if m == nil {
		return 0
	}
	n, _ := strconv.ParseFloat(m[2], 64)
	if m[1] != "" {
		experts, _ := strconv.ParseFloat(m[1], 64)
		n *= experts
	}
	if strings.EqualFold(m[3], "m") {
		n /= 1000
	}
	return n
}

// versionTag is the first number in a ref, and the family before it.
var versionTag = regexp.MustCompile(`^([^0-9]*)(\d+(?:\.\d+)*)`)

// version splits a ref into its family ("openai-codex/gpt-") and its version
// ([5 6] for "gpt-5.6-sol"). Refs with no number have no version.
func version(ref string) (string, []int) {
	m := versionTag.FindStringSubmatch(ref)
	if m == nil {
		return ref, nil
	}
	var v []int
	for _, part := range strings.Split(m[2], ".") {
		n, _ := strconv.Atoi(part)
		v = append(v, n)
	}
	return m[1], v
}

func tier(c Candidate) int {
	switch {
	case c.Local:
		return 0
	case c.Subscription:
		return 3
	case c.Cost.Input > 0 || c.Cost.Output > 0:
		return 2
	}
	return 1
}

func chatModel(ref string) bool {
	_, id := ai.SplitRef(ref)
	id = strings.ToLower(id)
	for _, s := range notChat {
		if strings.Contains(id, s) {
			return false
		}
	}
	return true
}

// fitting keeps the candidates whose context window has room for the
// conversation, with a fifth to spare for the reply. An unknown window fits.
func fitting(ladder []Candidate, tokens int) []Candidate {
	need := tokens + tokens/5
	var out []Candidate
	for _, c := range ladder {
		if c.ContextWindow == 0 || c.ContextWindow >= need {
			out = append(out, c)
		}
	}
	return out
}
