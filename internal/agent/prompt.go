package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/phucvinh57/pi-go/internal/tools"
)

// SystemPrompt describes the agent, lists the active tools and carries their
// usage guidelines, so the prompt always matches the tools actually enabled.
func SystemPrompt(ts []tools.Tool, cwd string, now time.Time) string {
	var list, guidelines []string
	seen := map[string]bool{}
	for _, t := range ts {
		s := t.Spec()
		list = append(list, fmt.Sprintf("- %s: %s", s.Name, s.Snippet))
		for _, g := range s.Guidelines {
			if !seen[g] {
				seen[g] = true
				guidelines = append(guidelines, "- "+g)
			}
		}
	}
	guidelines = append(guidelines,
		"- Act on your own: call tools to find things out instead of asking the user for paths or details. \"This directory\" means the current working directory, so explore it with the tools",
		"- Call tools through the tool calling interface; never write a tool call as text in your answer",
		"- Be concise in your responses",
		"- Show file paths clearly when working with files")

	var b strings.Builder
	b.WriteString("You are an expert coding assistant operating inside pi-go, a coding agent harness. " +
		"You help users by reading files, executing commands, editing code, and writing new files.\n\n")
	if len(list) > 0 {
		b.WriteString("Available tools:\n");b.WriteString(strings.Join(list, "\n"));b.WriteString("\n\n")
	}
	b.WriteString("Guidelines:\n");b.WriteString(strings.Join(guidelines, "\n"));b.WriteString("\n\n")
	fmt.Fprintf(&b, "Current date: %s\nCurrent working directory: %s", now.Format("2006-01-02"), cwd)
	return b.String()
}

// planInstructions is added to the system prompt while plan mode is on.
const planInstructions = `

Plan mode is on. You cannot change files or run commands: only the tools listed above are available. Explore the code with them, then answer with a concrete plan: a numbered list of steps that names the files to change and how to verify the result. Do not claim to have made any change. The user will approve the plan before anything is carried out.`

// PlanSystemPrompt is SystemPrompt for plan mode: it lists only the read-only
// tools of ts and tells the model to propose a plan instead of acting.
func PlanSystemPrompt(ts []tools.Tool, cwd string, now time.Time) string {
	base := SystemPrompt(tools.ReadOnly(ts), cwd, now)
	i := strings.Index(base, "\n\nCurrent date:")
	if i < 0 {
		return base + planInstructions
	}
	return base[:i] + planInstructions + base[i:]
}
