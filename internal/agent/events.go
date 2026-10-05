package agent

import "encoding/json"

// EventType names something that happens while a prompt runs.
type EventType string

const (
	EventText      EventType = "text"       // Text: more of the assistant's reply
	EventThinking  EventType = "thinking"   // Text: more of the model's reasoning
	EventToolStart EventType = "tool_start" // Tool, Args: a tool call is about to run
	EventToolEnd   EventType = "tool_end"   // Tool, Text, IsError: the call finished
)

// Event is one step of a running prompt, for a UI to show. Events arrive on the
// goroutine that called PromptWith, in order.
type Event struct {
	Type EventType
	// Text is a delta for text and thinking events, and the tool's output for
	// EventToolEnd.
	Text string
	// Tool is the tool name; Args its JSON arguments.
	Tool string
	Args json.RawMessage
	// IsError marks a failed tool call.
	IsError bool
}
