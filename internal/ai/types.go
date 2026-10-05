// Package ai talks to language models. It defines the message and streaming
// types every provider shares, and one adapter per wire protocol:
//
//   - openai-completions: OpenAI chat completions (Ollama)
//   - openai-codex-responses: the ChatGPT Codex Responses API (openai-codex)
//
// A provider turns a Context (system prompt, messages, tools) into a stream of
// Events that ends with exactly one terminal event carrying the finished
// assistant Message. Credentials are passed per call in Options, so the agent
// can refresh them between turns. The package does not import auth; callers
// resolve credentials and hand them in.
package ai

import (
	"encoding/json"
	"strings"
	"time"
)

// Role identifies who produced a Message.
type Role string

const (
	RoleUser       Role = "user"
	RoleAssistant  Role = "assistant"
	RoleToolResult Role = "toolResult"
)

// BlockType is the kind of a content Block.
type BlockType string

const (
	BlockText     BlockType = "text"
	BlockThinking BlockType = "thinking"
	BlockToolCall BlockType = "toolCall"
)

// Block is one piece of message content. Text and thinking blocks use Text;
// tool calls use ID, Name and Arguments (a JSON object).
type Block struct {
	Type      BlockType       `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// StopReason says why an assistant message ended.
type StopReason string

const (
	StopStop    StopReason = "stop"    // the model finished its turn
	StopLength  StopReason = "length"  // hit the output token limit
	StopToolUse StopReason = "toolUse" // the model wants tools run
	StopError   StopReason = "error"
	StopAborted StopReason = "aborted" // the caller cancelled the context
)

// Usage counts tokens for one assistant message. Input excludes CacheRead.
type Usage struct {
	Input       int `json:"input"`
	Output      int `json:"output"`
	CacheRead   int `json:"cacheRead"`
	TotalTokens int `json:"totalTokens"`
}

// Message is one entry of a conversation. Which fields apply depends on Role:
// the assistant fields describe the response, the tool result fields tie a
// result back to the call it answers. The flat shape is deliberate: a
// conversation round-trips through JSON unchanged, which sessions rely on.
type Message struct {
	Role    Role    `json:"role"`
	Content []Block `json:"content"`

	// Assistant only.
	Provider     string     `json:"provider,omitempty"`
	Model        string     `json:"model,omitempty"`
	Usage        Usage      `json:"usage,omitempty"`
	StopReason   StopReason `json:"stopReason,omitempty"`
	ErrorMessage string     `json:"errorMessage,omitempty"`

	// Tool result only.
	ToolCallID string `json:"toolCallId,omitempty"`
	ToolName   string `json:"toolName,omitempty"`
	IsError    bool   `json:"isError,omitempty"`

	Timestamp time.Time `json:"timestamp"`
}

// UserText builds a user message.
func UserText(text string) Message {
	return Message{Role: RoleUser, Content: []Block{{Type: BlockText, Text: text}}, Timestamp: time.Now()}
}

// ToolResult builds the message that answers a tool call.
func ToolResult(callID, toolName, text string, isError bool) Message {
	return Message{
		Role:       RoleToolResult,
		Content:    []Block{{Type: BlockText, Text: text}},
		ToolCallID: callID,
		ToolName:   toolName,
		IsError:    isError,
		Timestamp:  time.Now(),
	}
}

// Text joins the message's text blocks.
func (m Message) Text() string {
	var b strings.Builder
	for _, blk := range m.Content {
		if blk.Type == BlockText {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

// ToolCalls returns the message's tool call blocks in order.
func (m Message) ToolCalls() []Block {
	var calls []Block
	for _, blk := range m.Content {
		if blk.Type == BlockToolCall {
			calls = append(calls, blk)
		}
	}
	return calls
}

// Tool declares a function the model may call. Parameters is a JSON Schema
// object.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Context is everything the model sees for one request.
type Context struct {
	SystemPrompt string
	Messages     []Message
	Tools        []Tool
}

// Model identifies a model and how to reach it.
type Model struct {
	Provider string // "ollama", "openai-codex"
	ID       string // the provider's model name, e.g. "qwen2.5-coder:7b"
	API      string // wire protocol; picks the Provider implementation
	BaseURL  string
}

// Ref returns the "provider/id" form.
func (m Model) Ref() string { return m.Provider + "/" + m.ID }

// Options are per-request settings.
type Options struct {
	APIKey      string
	MaxTokens   int      // 0 means the provider default
	Temperature *float64 // nil means the provider default
	// Reasoning is a provider-neutral effort level ("minimal", "low",
	// "medium", "high"); empty leaves the model's default.
	Reasoning string
	// SessionID lets providers that cache prompts group related requests.
	SessionID string
}
