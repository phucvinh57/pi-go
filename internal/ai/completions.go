package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// APICompletions is the OpenAI chat completions protocol, which Ollama serves
// under /v1.
const APICompletions = "openai-completions"

// completions is the wire for APICompletions.
type completions struct{}

func (completions) Request(m Model, c Context, o Options) (httpRequest, error) {
	headers := map[string]string{}
	if o.APIKey != "" {
		headers["Authorization"] = "Bearer " + o.APIKey
	}
	return httpRequest{
		URL:     strings.TrimRight(m.BaseURL, "/") + "/chat/completions",
		Headers: headers,
		Body:    completionsBody(m, c, o),
	}, nil
}

func (completions) NewDecoder(b *builder) decoder {
	return &completionsDecoder{b: b, reason: StopStop, toolIndex: -1}
}

// --- request ---

type cRequest struct {
	Model           string          `json:"model"`
	Messages        []cMessage      `json:"messages"`
	Stream          bool            `json:"stream"`
	StreamOptions   *cStreamOptions `json:"stream_options,omitempty"`
	Tools           []cTool         `json:"tools,omitempty"`
	MaxTokens       int             `json:"max_tokens,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
}

type cStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type cMessage struct {
	Role       string      `json:"role"`
	Content    string      `json:"content"`
	ToolCalls  []cToolCall `json:"tool_calls,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
}

type cToolCall struct {
	ID       string        `json:"id"`
	Type     string        `json:"type"`
	Function cFunctionCall `json:"function"`
}

type cFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type cTool struct {
	Type     string    `json:"type"`
	Function cFunction `json:"function"`
}

type cFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

func completionsBody(m Model, c Context, o Options) cRequest {
	req := cRequest{
		Model:           m.ID,
		Messages:        completionsMessages(c),
		Stream:          true,
		StreamOptions:   &cStreamOptions{IncludeUsage: true},
		MaxTokens:       o.MaxTokens,
		Temperature:     o.Temperature,
		ReasoningEffort: o.Reasoning,
	}
	for _, t := range c.Tools {
		req.Tools = append(req.Tools, cTool{
			Type:     "function",
			Function: cFunction{Name: t.Name, Description: t.Description, Parameters: t.Parameters},
		})
	}
	return req
}

// completionsMessages converts the conversation. Thinking blocks are dropped:
// the protocol has no portable way to send them back. Assistant messages that
// ended in error or abort are skipped, since they are partial and replaying
// them would confuse the model.
func completionsMessages(c Context) []cMessage {
	var out []cMessage
	if c.SystemPrompt != "" {
		out = append(out, cMessage{Role: "system", Content: c.SystemPrompt})
	}
	for _, msg := range c.Messages {
		switch msg.Role {
		case RoleUser:
			out = append(out, cMessage{Role: "user", Content: msg.Text()})
		case RoleToolResult:
			out = append(out, cMessage{Role: "tool", ToolCallID: msg.ToolCallID, Content: msg.Text()})
		case RoleAssistant:
			if msg.StopReason == StopError || msg.StopReason == StopAborted {
				continue
			}
			out = append(out, completionsAssistant(msg))
		}
	}
	return out
}

func completionsAssistant(msg Message) cMessage {
	am := cMessage{Role: "assistant", Content: msg.Text()}
	for _, call := range msg.ToolCalls() {
		am.ToolCalls = append(am.ToolCalls, cToolCall{
			ID:       call.ID,
			Type:     "function",
			Function: cFunctionCall{Name: call.Name, Arguments: string(call.Arguments)},
		})
	}
	return am
}

// --- response ---

// cChunk is one streamed chat.completion.chunk.
type cChunk struct {
	Choices []cChoice `json:"choices"`
	Usage   *cUsage   `json:"usage"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type cChoice struct {
	Delta        cDelta `json:"delta"`
	FinishReason string `json:"finish_reason"`
}

type cDelta struct {
	Content string `json:"content"`
	// Reasoning models stream their thinking under one of two names.
	ReasoningContent string           `json:"reasoning_content"`
	Reasoning        string           `json:"reasoning"`
	ToolCalls        []cToolCallDelta `json:"tool_calls"`
}

type cToolCallDelta struct {
	Index    int           `json:"index"`
	ID       string        `json:"id"`
	Function cFunctionCall `json:"function"`
}

type cUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptCacheHit      int `json:"prompt_cache_hit_tokens"` // DeepSeek-style servers
	PromptTokensDetails *struct {
		CachedTokens     int `json:"cached_tokens"`
		CacheWriteTokens int `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
}

type completionsDecoder struct {
	b        *builder
	reason   StopReason
	finished bool // saw a finish_reason or [DONE]
	rawStop  string

	// The tool call being streamed. A call is identified by its index and ID.
	toolIndex int
	toolID    string
	toolSeq   int // counts calls, to name the ones the server leaves anonymous
}

func (d *completionsDecoder) Event(data string) error {
	if data == "[DONE]" {
		d.finished = true
		return nil
	}
	var chunk cChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return fmt.Errorf("decode stream chunk: %w", err)
	}
	if chunk.Error != nil {
		return fmt.Errorf("provider error: %s", chunk.Error.Message)
	}
	if chunk.Usage != nil {
		d.usage(chunk.Usage)
	}
	for _, choice := range chunk.Choices {
		d.choice(choice)
	}
	return nil
}

func (d *completionsDecoder) End() (StopReason, error) {
	if !d.finished {
		return "", errors.New("stream ended before the response was complete")
	}
	if d.reason == StopError {
		return "", fmt.Errorf("response ended with finish_reason %q", d.rawStop)
	}
	return d.reason, nil
}

func (d *completionsDecoder) usage(u *cUsage) {
	cacheRead, cacheWrite := u.PromptCacheHit, 0
	if u.PromptTokensDetails != nil {
		if u.PromptTokensDetails.CachedTokens > 0 {
			cacheRead = u.PromptTokensDetails.CachedTokens
		}
		cacheWrite = u.PromptTokensDetails.CacheWriteTokens
	}
	usage := Usage{
		Input:      max(0, u.PromptTokens-cacheRead-cacheWrite),
		Output:     u.CompletionTokens,
		CacheRead:  cacheRead,
		CacheWrite: cacheWrite,
	}
	// Servers disagree on whether total_tokens exists or counts the cache, so
	// the total is the sum of the parts, which is what a context holds.
	usage.TotalTokens = usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
	d.b.msg.Usage = usage
}

func (d *completionsDecoder) choice(c cChoice) {
	if s := c.Delta.ReasoningContent + c.Delta.Reasoning; s != "" {
		d.b.Thinking(s)
	}
	if c.Delta.Content != "" {
		d.b.Text(c.Delta.Content)
	}
	for _, tc := range c.Delta.ToolCalls {
		d.toolCall(tc)
	}
	if c.FinishReason != "" {
		d.reason = completionsStopReason(c.FinishReason)
		d.rawStop = c.FinishReason
		d.finished = true
	}
}

func (d *completionsDecoder) toolCall(tc cToolCallDelta) {
	// A new call starts when the index moves or a different ID shows up (some
	// servers send index 0 for every call).
	if d.b.open != BlockToolCall || tc.Index != d.toolIndex || (tc.ID != "" && tc.ID != d.toolID) {
		d.toolSeq++
		d.toolIndex = tc.Index
		d.toolID = tc.ID
		if d.toolID == "" {
			d.toolID = fmt.Sprintf("call_%d", d.toolSeq)
		}
		d.b.BeginToolCall(d.toolID, tc.Function.Name)
	}
	d.b.ToolCallArgs(tc.Function.Arguments)
}

func completionsStopReason(r string) StopReason {
	switch r {
	case "stop":
		return StopStop
	case "length":
		return StopLength
	case "tool_calls", "function_call":
		return StopToolUse
	default:
		return StopError
	}
}
