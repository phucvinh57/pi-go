// Package tools implements the built-in tools the agent can call: read, bash,
// edit and write. Each tool declares itself with a Spec (name, description,
// JSON Schema, system prompt text) and runs from raw JSON arguments, which is
// what a model produces.
//
// The package knows nothing about models or the agent loop. The loop turns a
// Spec into an ai.Tool, calls Execute for each tool call the model makes, and
// sends the outcome back: a Result becomes a tool result message, an error
// becomes one with IsError set, its message being what the model sees. Tools
// therefore word their errors for the model ("Found 2 occurrences ... provide
// more context"), not for a log.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// Spec describes a tool to the model and to the system prompt.
type Spec struct {
	Name        string
	Description string
	// Parameters is the JSON Schema of the arguments, an object.
	Parameters json.RawMessage
	// Snippet is the one-line entry in the system prompt's list of tools.
	Snippet string
	// Guidelines are bullets added to the system prompt while the tool is on.
	Guidelines []string
	// ReadOnly says the tool never changes anything, so it stays available in
	// plan mode.
	ReadOnly bool
}

// Result is what a successful tool run returns.
type Result struct {
	// Text is what the model sees.
	Text string
	// Details is extra structured data for UIs (a diff, truncation info). It
	// is never sent to the model. Nil when there is none.
	Details any
}

// Tool is something the model can call.
type Tool interface {
	Spec() Spec
	// Execute runs the tool with the model's JSON arguments. A non-nil error
	// means the call failed and its message goes back to the model. onUpdate,
	// when non-nil, receives partial output while a long call runs (bash).
	// Cancelling ctx stops the work promptly.
	Execute(ctx context.Context, args json.RawMessage, onUpdate func(Result)) (Result, error)
}

// Core returns the four built-in tools, rooted at cwd: relative paths resolve
// against it and bash runs in it.
func Core(cwd string) []Tool {
	return []Tool{NewRead(cwd), NewBash(cwd), NewEdit(cwd), NewWrite(cwd)}
}

// ReadOnly returns the tools of ts that do not change anything, in order.
func ReadOnly(ts []Tool) []Tool {
	var out []Tool
	for _, t := range ts {
		if t.Spec().ReadOnly {
			out = append(out, t)
		}
	}
	return out
}

// Find returns the tool with the given name.
func Find(ts []Tool, name string) (Tool, bool) {
	for _, t := range ts {
		if t.Spec().Name == name {
			return t, true
		}
	}
	return nil, false
}

// decodeArgs unmarshals a tool call's arguments. Models send "" or null for a
// tool with no meaningful arguments, which counts as an empty object.
func decodeArgs(raw json.RawMessage, v any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage("{}")
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}
