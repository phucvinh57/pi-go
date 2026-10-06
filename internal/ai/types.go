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

// Usage counts tokens, and what they cost, for one assistant message. Input
// excludes CacheRead and CacheWrite, so the four counts never overlap.
type Usage struct {
	Input       int  `json:"input"`
	Output      int  `json:"output"`
	CacheRead   int  `json:"cacheRead"`
	CacheWrite  int  `json:"cacheWrite"`
	TotalTokens int  `json:"totalTokens"`
	Cost        Cost `json:"cost"`
}

// Cost is what a message's tokens cost, in dollars.
type Cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
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

	// ContextWindow and MaxTokens are 0 when unknown.
	ContextWindow int
	MaxTokens     int
	// Cost is the price of the model. The zero value means free or unpriced.
	Cost Rates
}

// Rates are prices in dollars per million tokens.
type Rates struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
}

// Ref returns the "provider/id" form.
func (m Model) Ref() string { return m.Provider + "/" + m.ID }

// EffortLevels are provider-neutral levels, from least to most reasoning.
// Providers translate these to their own vocabulary at the request boundary.
// Models that cannot reason may ignore or reject the resulting field.
var EffortLevels = []string{"low", "medium", "high", "xhigh", "max"}

// ValidEffort reports whether level can be used as Options.Reasoning. The empty
// string, meaning the model's default, is valid.
func ValidEffort(level string) bool {
	if level == "" {
		return true
	}
	for _, l := range EffortLevels {
		if l == level {
			return true
		}
	}
	return false
}

// Options are per-request settings.
type Options struct {
	APIKey      string
	MaxTokens   int      // 0 means the provider default
	Temperature *float64 // nil means the provider default
	// Reasoning is a provider-neutral effort level, one of EffortLevels; empty
	// leaves the model's default.
	Reasoning string
	// SessionID lets providers that cache prompts group related requests.
	SessionID string
}
