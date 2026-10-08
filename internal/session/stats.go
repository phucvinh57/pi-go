package session

import (
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/phucvinh57/pi-go/internal/ai"
)

// Filter narrows what Summarize counts.
type Filter struct {
	// Since drops everything before it; the zero time keeps everything. It
	// applies to each message, so a session that straddles it counts only for
	// the part after.
	Since time.Time
	// CWD keeps only the sessions started in this directory; empty keeps all.
	CWD string
	// Loc decides which calendar day a message belongs to; nil means
	// time.Local.
	Loc *time.Location
	// Subscription says whether a provider is paid by a subscription. nil
	// means none is.
	Subscription func(provider string) bool
	// Now stamps the summary; the zero value means time.Now().
	Now time.Time
}

// recentLimit is how many sessions Summary.Recent lists.
const recentLimit = 50

// firstPromptRunes is how much of a session's first prompt is kept.
const firstPromptRunes = 80

// Tokens counts tokens by kind. Input excludes the cached kinds.
type Tokens struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
}

// Total is every token counted.
func (t Tokens) Total() int { return t.Input + t.Output + t.CacheRead + t.CacheWrite }

// Prompt is the tokens sent to the model: new, cached, and written to the cache.
func (t Tokens) Prompt() int { return t.Input + t.CacheRead + t.CacheWrite }

func (t *Tokens) add(u ai.Usage) {
	t.Input += u.Input
	t.Output += u.Output
	t.CacheRead += u.CacheRead
	t.CacheWrite += u.CacheWrite
}

// Overview is the totals over everything counted.
type Overview struct {
	Sessions    int     `json:"sessions"`
	Prompts     int     `json:"prompts"`     // user messages
	Calls       int     `json:"calls"`       // model calls, failed ones included
	FailedCalls int     `json:"failedCalls"` // calls that ended in an error or were aborted
	ToolCalls   int     `json:"toolCalls"`
	Tokens      Tokens  `json:"tokens"`
	TotalTokens int     `json:"totalTokens"`
	Cost        float64 `json:"cost"` // dollars
	// SubscriptionCost is the part of Cost that came from subscription
	// providers, which is what the tokens would cost and not a bill.
	SubscriptionCost float64 `json:"subscriptionCost"`
	// CacheHit is the share of prompt tokens read from the cache, in percent;
	// negative when no call had a prompt.
	CacheHit float64   `json:"cacheHit"`
	First    time.Time `json:"first,omitempty"`
	Last     time.Time `json:"last,omitempty"`
}

// Day is one calendar day of activity.
type Day struct {
	Date     string  `json:"date"` // 2006-01-02, in the filter's location
	Sessions int     `json:"sessions"`
	Prompts  int     `json:"prompts"`
	Calls    int     `json:"calls"`
	Tokens   Tokens  `json:"tokens"`
	Cost     float64 `json:"cost"`
}

// ModelStat is the usage of one model.
type ModelStat struct {
	Ref          string  `json:"ref"` // "provider/id"
	Provider     string  `json:"provider"`
	Calls        int     `json:"calls"`
	FailedCalls  int     `json:"failedCalls"`
	Tokens       Tokens  `json:"tokens"`
	TotalTokens  int     `json:"totalTokens"`
	Cost         float64 `json:"cost"`
	Subscription bool    `json:"subscription"`
	// Share is this model's share of all tokens, in percent.
	Share float64 `json:"share"`
}

// ProjectStat is the usage of the sessions started in one directory.
type ProjectStat struct {
	CWD         string  `json:"cwd"`
	Sessions    int     `json:"sessions"`
	Calls       int     `json:"calls"`
	TotalTokens int     `json:"totalTokens"`
	Cost        float64 `json:"cost"`
}

// ToolStat is how often a tool ran and how often it failed.
type ToolStat struct {
	Name   string `json:"name"`
	Calls  int    `json:"calls"`
	Errors int    `json:"errors"`
}

// SessionStat is one session, for the list of recent ones.
type SessionStat struct {
	ID          string    `json:"id"`
	Start       time.Time `json:"start"`
	CWD         string    `json:"cwd"`
	Path        string    `json:"path"`
	Models      []string  `json:"models"`
	FirstPrompt string    `json:"firstPrompt"`
	Prompts     int       `json:"prompts"`
	Calls       int       `json:"calls"`
	TotalTokens int       `json:"totalTokens"`
	Cost        float64   `json:"cost"`
}

// Summary is everything the statistics page shows.
type Summary struct {
	GeneratedAt time.Time     `json:"generatedAt"`
	Since       time.Time     `json:"since,omitempty"`
	CWD         string        `json:"cwd,omitempty"`
	Overview    Overview      `json:"overview"`
	Days        []Day         `json:"days"` // oldest first, quiet days in between included
	Models      []ModelStat   `json:"models"`
	Projects    []ProjectStat `json:"projects"`
	Tools       []ToolStat    `json:"tools"`
	Recent      []SessionStat `json:"recent"` // newest first
	// Skipped counts lines in session files that could not be read.
	Skipped int `json:"skipped"`
}

// Summarize counts what the sessions used. The numbers are the ones recorded
// when each call was made, cost included, so a price that changed since is not
// applied backwards.
func Summarize(sessions []*Session, f Filter) Summary {
	loc := f.Loc
	if loc == nil {
		loc = time.Local
	}
	now := f.Now
	if now.IsZero() {
		now = time.Now()
	}
	isSub := f.Subscription
	if isSub == nil {
		isSub = func(string) bool { return false }
	}

	var (
		sum      = Summary{GeneratedAt: now, Since: f.Since, CWD: f.CWD}
		days     = map[string]*Day{}
		daySess  = map[string]map[string]bool{}
		models   = map[string]*ModelStat{}
		projects = map[string]*ProjectStat{}
		tools    = map[string]*ToolStat{}
		ov       = &sum.Overview
	)

	for _, s := range sessions {
		if f.CWD != "" && s.Header.CWD != f.CWD {
			continue
		}
		sum.Skipped += s.Skipped

		stat := SessionStat{ID: s.Header.ID, Start: s.Header.Time(), CWD: s.Header.CWD, Path: s.Path}
		seenModel := map[string]bool{}
		addModel := func(ref string) {
			if !seenModel[ref] {
				seenModel[ref] = true
				stat.Models = append(stat.Models, ref)
			}
		}

		for _, e := range s.Entries {
			t := e.Time()
			if e.Type == TypeModelChange {
				if !t.Before(f.Since) {
					addModel(e.Provider + "/" + e.ModelID)
				}
				continue
			}
			if e.Type != TypeMessage {
				continue
			}
			m := e.Message
			if m == nil || t.Before(f.Since) {
				continue
			}
			day := t.In(loc).Format("2006-01-02")
			d := days[day]
			if d == nil {
				d = &Day{Date: day}
				days[day] = d
				daySess[day] = map[string]bool{}
			}
			if !daySess[day][s.Header.ID] {
				daySess[day][s.Header.ID] = true
				d.Sessions++
			}
			if ov.First.IsZero() || t.Before(ov.First) {
				ov.First = t
			}
			if t.After(ov.Last) {
				ov.Last = t
			}

			switch m.Role {
			case ai.RoleUser:
				ov.Prompts++
				d.Prompts++
				stat.Prompts++
				if stat.FirstPrompt == "" {
					stat.FirstPrompt = excerpt(m.Text(), firstPromptRunes)
				}

			case ai.RoleAssistant:
				ref := modelRef(m)
				addModel(ref)
				u := m.Usage
				ov.Calls++
				d.Calls++
				stat.Calls++
				ov.Tokens.add(u)
				d.Tokens.add(u)
				ov.Cost += u.Cost.Total
				d.Cost += u.Cost.Total
				stat.TotalTokens += tokenSum(u)
				stat.Cost += u.Cost.Total

				ms := models[ref]
				if ms == nil {
					ms = &ModelStat{Ref: ref, Provider: m.Provider, Subscription: isSub(m.Provider)}
					models[ref] = ms
				}
				ms.Calls++
				ms.Tokens.add(u)
				ms.Cost += u.Cost.Total
				if ms.Subscription {
					ov.SubscriptionCost += u.Cost.Total
				}
				if m.StopReason == ai.StopError || m.StopReason == ai.StopAborted {
					ov.FailedCalls++
					ms.FailedCalls++
				}

			case ai.RoleToolResult:
				ov.ToolCalls++
				ts := tools[m.ToolName]
				if ts == nil {
					ts = &ToolStat{Name: m.ToolName}
					tools[m.ToolName] = ts
				}
				ts.Calls++
				if m.IsError {
					ts.Errors++
				}
			}
		}

		if stat.Prompts == 0 && stat.Calls == 0 {
			continue // nothing in range
		}
		ov.Sessions++
		p := projects[s.Header.CWD]
		if p == nil {
			p = &ProjectStat{CWD: s.Header.CWD}
			projects[s.Header.CWD] = p
		}
		p.Sessions++
		p.Calls += stat.Calls
		p.TotalTokens += stat.TotalTokens
		p.Cost += stat.Cost
		sum.Recent = append(sum.Recent, stat)
	}

	ov.TotalTokens = ov.Tokens.Total()
	ov.CacheHit = -1
	if p := ov.Tokens.Prompt(); p > 0 {
		ov.CacheHit = float64(ov.Tokens.CacheRead) / float64(p) * 100
	}

	for _, ms := range models {
		ms.TotalTokens = ms.Tokens.Total()
		if ov.TotalTokens > 0 {
			ms.Share = float64(ms.TotalTokens) / float64(ov.TotalTokens) * 100
		}
		sum.Models = append(sum.Models, *ms)
	}
	sort.Slice(sum.Models, func(i, j int) bool {
		a, b := sum.Models[i], sum.Models[j]
		switch {
		case a.Cost != b.Cost:
			return a.Cost > b.Cost
		case a.TotalTokens != b.TotalTokens:
			return a.TotalTokens > b.TotalTokens
		}
		return a.Ref < b.Ref
	})

	for _, p := range projects {
		sum.Projects = append(sum.Projects, *p)
	}
	sort.Slice(sum.Projects, func(i, j int) bool {
		a, b := sum.Projects[i], sum.Projects[j]
		switch {
		case a.Cost != b.Cost:
			return a.Cost > b.Cost
		case a.TotalTokens != b.TotalTokens:
			return a.TotalTokens > b.TotalTokens
		}
		return a.CWD < b.CWD
	})

	for _, t := range tools {
		sum.Tools = append(sum.Tools, *t)
	}
	sort.Slice(sum.Tools, func(i, j int) bool {
		a, b := sum.Tools[i], sum.Tools[j]
		if a.Calls != b.Calls {
			return a.Calls > b.Calls
		}
		return a.Name < b.Name
	})

	sum.Days = fillDays(days, loc)

	sort.SliceStable(sum.Recent, func(i, j int) bool { return sum.Recent[i].Start.After(sum.Recent[j].Start) })
	if len(sum.Recent) > recentLimit {
		sum.Recent = sum.Recent[:recentLimit]
	}
	return sum
}

func tokenSum(u ai.Usage) int { return u.Input + u.Output + u.CacheRead + u.CacheWrite }

// modelRef names the model of an assistant message as "provider/id".
func modelRef(m *ai.Message) string {
	if m.Provider == "" && m.Model == "" {
		return "unknown"
	}
	return m.Provider + "/" + m.Model
}

// fillDays lists the days oldest first, with the quiet days between the first
// and last as empty entries, so a chart shows gaps as gaps.
func fillDays(days map[string]*Day, loc *time.Location) []Day {
	if len(days) == 0 {
		return nil
	}
	keys := make([]string, 0, len(days))
	for k := range days {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	first, _ := time.ParseInLocation("2006-01-02", keys[0], loc)
	last, _ := time.ParseInLocation("2006-01-02", keys[len(keys)-1], loc)
	var out []Day
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		if day, ok := days[key]; ok {
			out = append(out, *day)
		} else {
			out = append(out, Day{Date: key})
		}
	}
	return out
}

// excerpt collapses whitespace and cuts s to at most n runes, ending in an
// ellipsis when it was cut.
func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:n-1])) + "…"
}
