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
		b.WriteString("Available tools:\n" + strings.Join(list, "\n") + "\n\n")
	}
	b.WriteString("Guidelines:\n" + strings.Join(guidelines, "\n") + "\n\n")
	fmt.Fprintf(&b, "Current date: %s\nCurrent working directory: %s", now.Format("2006-01-02"), cwd)
	return b.String()
}
