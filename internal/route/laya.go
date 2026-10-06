package route

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/phucvinh57/pi-go/internal/ai"
)

// Limits on what the classifier is shown, in runes.
const (
	maxPromptRunes = 16_000
	maxReplyRunes  = 1_000
)

// Laya rates prompts with a classifier model (Laya, served locally by
// laya-serve), the way PI's Jev routers do: narrow typed questions in one
// forward pass, which code then turns into a model and an effort. Any server
// that speaks System One works the same.
//
// What it is asked was tried against laya-serve's English checkpoint. It is
// shown only the prompt and the previous reply: plan mode and the size of the
// conversation made its answers worse, and the router weighs those itself. A
// yes/no "would deep reasoning help?" was left out because its answer barely
// moved between a typo and a deadlock, in any wording.
type Laya struct {
	Classifier ai.Classifier
	Model      ai.Model
	Options    ai.Options // the API key
	// Timeout is DefaultTimeout when 0.
	Timeout time.Duration
}

// layaQuestions are what Laya is asked about every prompt.
var layaQuestions = map[string]ai.Question{
	"task_kind": ai.Choice("What kind of software engineering work does `prompt` ask for?", map[string]string{
		"plan":      "Design an approach or write a plan before any code is changed",
		"implement": "Write new code or add a feature",
		"debug":     "Find and fix a bug, an error or a failing test",
		"refactor":  "Restructure or rename existing code without changing what it does",
		"review":    "Review code or a change for problems",
		"explain":   "Answer a question about code or explain how it works, changing nothing",
		"chat":      "Small talk, thanks, or a trivial request",
	}),
	"complexity": ai.Score("How demanding is the work `prompt` asks for?",
		"trivial: a one-line answer or change",
		"ordinary: a routine fix, feature or question",
		"demanding: several files, subtle logic or hard debugging",
		"architectural: cross-cutting design or a large refactor"),
}

// Assess asks Laya about the prompt.
func (j Laya) Assess(ctx context.Context, req Request) (Assessment, error) {
	timeout := j.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	state := map[string]any{"prompt": truncate(stripFiles(req.Prompt), maxPromptRunes)}
	// The previous reply tells what "go ahead with that plan" means.
	if req.LastReply != "" {
		state["previous_reply"] = truncate(req.LastReply, maxReplyRunes)
	}
	res, err := j.Classifier.Classify(ctx, j.Model, state, layaQuestions, j.Options)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return Assessment{}, fmt.Errorf("%s did not answer within %s", j.Model.Ref(), timeout)
		}
		return Assessment{}, fmt.Errorf("%s: %w", j.Model.Ref(), err)
	}
	kind, cx := res.Answers["task_kind"], res.Answers["complexity"]
	return Assessment{
		Kind:       kind.Choice,
		Demand:     cx.Score,
		Confidence: cx.Confidence,
		Usage:      res.Usage,
		UsageRef:   j.Model.Ref(),
	}, nil
}

// fileBlock is a file the user tagged with @, as the TUI appends it to the
// prompt.
var fileBlock = regexp.MustCompile(`(?s)<file path="((?:[^"\\]|\\.)*)">\n(.*?)\n</file>`)

// stripFiles replaces the content of tagged files with a one-line mention: the
// router needs to know a file is there, not to read it.
func stripFiles(prompt string) string {
	return fileBlock.ReplaceAllStringFunc(prompt, func(block string) string {
		m := fileBlock.FindStringSubmatch(block)
		return fmt.Sprintf("[file %s attached, %d lines]", m[1], strings.Count(m[2], "\n")+1)
	})
}

// countFiles is how many tagged files the prompt carries.
func countFiles(prompt string) int { return len(fileBlock.FindAllStringIndex(prompt, -1)) }

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
