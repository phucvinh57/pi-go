package tui

import (
	"encoding/json"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

type EventKind int

const (
	EventText      EventKind = iota // Text: more of the reply
	EventThinking                   // Text: more of the model's reasoning
	EventToolStart                  // Tool, Args: a tool call begins
	EventToolEnd                    // Tool, Text, IsError: the call finished
	EventStats                      // Stats: the session's usage or size changed
	EventWarning                    // Text: something went wrong that does not stop the prompt
)

// Event is progress from a running prompt: the reply as it streams, and the
// tool calls the agent makes on the way.
type Event struct {
	Kind    EventKind
	Text    string
	Tool    string
	Args    string // JSON
	IsError bool
	Stats   *Stats
}

const (
	maxPreviewLines = 6  // lines of tool output shown under a call
	maxArgChars     = 80 // width of the argument summary after a tool name
)

// eventMsg carries an Event from the prompt's goroutine into Update.
type eventMsg struct{ ev Event }

// clean makes model text safe to print: no escape sequences, carriage returns
// or tabs, which would corrupt the inline redraw or the width arithmetic.
func clean(s string) string {
	s = ansi.Strip(s)
	s = strings.ReplaceAll(s, "\r", "")
	return strings.ReplaceAll(s, "\t", "    ")
}

// summarizeArgs describes a call on one line: the command of bash, the path of
// the file tools, otherwise the compact JSON.
func summarizeArgs(raw string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return oneLine(raw)
	}
	for _, key := range []string{"command", "path"} {
		if v, ok := args[key].(string); ok && v != "" {
			if key == "command" {
				v = "$ " + v
			}
			return oneLine(v)
		}
	}
	if len(args) == 0 {
		return ""
	}
	return oneLine(raw)
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(clean(s)), " ")
	return ansi.Truncate(s, maxArgChars, "…")
}
