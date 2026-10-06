package tui

import (
	"fmt"
	"strings"
)

// Stats is a snapshot of the session's usage, for the footer and /session. It
// mirrors what the agent counts, because tui does not import agent.
type Stats struct {
	// Counts of the conversation as it stands.
	UserMessages      int
	AssistantMessages int
	ToolCalls         int
	ToolResults       int

	// Tokens of every model call made, failed ones included. Input excludes the
	// cached counts.
	Input      int
	Output     int
	CacheRead  int
	CacheWrite int
	Cost       float64 // dollars

	ByModel []ModelCost // highest cost first

	// CacheHit is the share of the last call's prompt read from the cache, in
	// percent, or negative when there was no call.
	CacheHit float64

	// Subscription means the cost is what the tokens would cost, not a bill.
	Subscription bool

	ContextTokens  int
	ContextWindow  int     // 0 when unknown
	ContextPercent float64 // 0 when the window is unknown
}

// ModelCost is the cost of the calls made to one model.
type ModelCost struct {
	Ref  string
	Cost float64
}

// Context usage turns the footer's context figure to the warning colour above
// the first threshold and to the error colour above the second.
const (
	ctxWarnPercent  = 70
	ctxErrorPercent = 90
)

// formatTokens writes a token count compactly: 950, 1.2k, 45k, 1.5M.
func formatTokens(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprint(n)
	case n < 10_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	case n < 1_000_000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	case n < 10_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	return fmt.Sprintf("%dM", (n+500_000)/1_000_000)
}

// groupDigits writes n with thousands separators: 1,234,567.
func groupDigits(n int) string {
	s := fmt.Sprint(n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 && s[i-1] != '-' {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// usageParts lists the footer's usage figures, without the context: tokens in
// and out, cache traffic, the cache hit rate and the cost. Figures that are
// zero are left out, so a fresh session shows nothing.
func (s *Stats) usageParts() []string {
	var parts []string
	var tokens []string
	if s.Input > 0 {
		tokens = append(tokens, "↑"+formatTokens(s.Input))
	}
	if s.Output > 0 {
		tokens = append(tokens, "↓"+formatTokens(s.Output))
	}
	if s.CacheRead > 0 {
		tokens = append(tokens, "R"+formatTokens(s.CacheRead))
	}
	if s.CacheWrite > 0 {
		tokens = append(tokens, "W"+formatTokens(s.CacheWrite))
	}
	if len(tokens) > 0 {
		parts = append(parts, strings.Join(tokens, " "))
	}
	if (s.CacheRead > 0 || s.CacheWrite > 0) && s.CacheHit >= 0 {
		parts = append(parts, fmt.Sprintf("CH%.1f%%", s.CacheHit))
	}
	if s.Cost > 0 || s.Subscription {
		cost := fmt.Sprintf("$%.3f", s.Cost)
		if s.Subscription {
			cost += " (sub)"
		}
		parts = append(parts, cost)
	}
	return parts
}

// contextPart is the footer's context figure: "ctx 41.2%/272k" when the window
// is known, "ctx 12k" when only the size is. It is empty when there is nothing
// to say.
func (s *Stats) contextPart() string {
	switch {
	case s.ContextWindow > 0:
		return fmt.Sprintf("ctx %.1f%%/%s", s.ContextPercent, formatTokens(s.ContextWindow))
	case s.ContextTokens > 0:
		return "ctx " + formatTokens(s.ContextTokens)
	}
	return ""
}

// report is the text of /session.
func (s *Stats) report() string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	line("Messages")
	line("  user %d · assistant %d · tool calls %d · tool results %d",
		s.UserMessages, s.AssistantMessages, s.ToolCalls, s.ToolResults)

	prompt := s.Input + s.CacheRead + s.CacheWrite
	line("")
	line("Tokens")
	line("  input    %s", groupDigits(prompt))
	cached := fmt.Sprintf("  cached   %s", groupDigits(s.CacheRead))
	if prompt > 0 && s.CacheRead > 0 {
		cached += fmt.Sprintf(" (%.1f%% of input)", float64(s.CacheRead)/float64(prompt)*100)
	}
	line("%s", cached)
	uncached := fmt.Sprintf("  uncached %s", groupDigits(s.Input+s.CacheWrite))
	if s.CacheWrite > 0 {
		uncached += fmt.Sprintf(" (%s written to the cache)", groupDigits(s.CacheWrite))
	}
	line("%s", uncached)
	line("  output   %s", groupDigits(s.Output))
	line("  total    %s", groupDigits(prompt+s.Output))

	line("")
	if s.Subscription {
		line("Cost  $%.4f (subscription: what these tokens would cost, not a bill)", s.Cost)
	} else {
		line("Cost  $%.4f", s.Cost)
	}
	if len(s.ByModel) > 1 {
		for _, mc := range s.ByModel {
			line("  %-32s $%.4f", mc.Ref, mc.Cost)
		}
	}

	line("")
	switch {
	case s.ContextWindow > 0:
		line("Context  %.1f%% of %s (%s tokens)", s.ContextPercent, groupDigits(s.ContextWindow), groupDigits(s.ContextTokens))
	default:
		line("Context  %s tokens (window unknown: add contextWindow to models.json)", groupDigits(s.ContextTokens))
	}
	return strings.TrimRight(b.String(), "\n")
}
