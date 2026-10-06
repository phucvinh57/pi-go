package ai

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// APICodexResponses is the ChatGPT backend's Responses API, used with a
// ChatGPT Plus/Pro login (provider "openai-codex").
const APICodexResponses = "openai-codex-responses"

// DefaultCodexBaseURL is where the Codex backend lives.
const DefaultCodexBaseURL = "https://chatgpt.com/backend-api"

const codexJWTClaim = "https://api.openai.com/auth"

// codex is the wire for APICodexResponses.
type codex struct{}

func (codex) Request(m Model, c Context, o Options) (httpRequest, error) {
	if o.APIKey == "" {
		return httpRequest{}, errors.New("no access token for openai-codex (run `pi-go auth login openai-codex`)")
	}
	accountID, err := codexAccountID(o.APIKey)
	if err != nil {
		return httpRequest{}, err
	}
	headers := map[string]string{
		"Authorization":      "Bearer " + o.APIKey,
		"chatgpt-account-id": accountID,
		"originator":         "pi",
		"User-Agent":         "pi-go",
		"OpenAI-Beta":        "responses=experimental",
	}
	if o.SessionID != "" {
		headers["session-id"] = o.SessionID
	}
	return httpRequest{URL: codexURL(m.BaseURL), Headers: headers, Body: codexBody(m, c, o)}, nil
}

func (codex) NewDecoder(b *builder) decoder {
	return &codexDecoder{b: b, reason: StopStop}
}

// --- request ---

type rRequest struct {
	Model             string      `json:"model"`
	Store             bool        `json:"store"`
	Stream            bool        `json:"stream"`
	Instructions      string      `json:"instructions"`
	Input             []any       `json:"input"`
	Tools             []rTool     `json:"tools,omitempty"`
	ToolChoice        string      `json:"tool_choice"`
	ParallelToolCalls bool        `json:"parallel_tool_calls"`
	Temperature       *float64    `json:"temperature,omitempty"`
	Reasoning         *rReasoning `json:"reasoning,omitempty"`
	PromptCacheKey    string      `json:"prompt_cache_key,omitempty"`
}

type rReasoning struct {
	Effort  string `json:"effort"`
	Summary string `json:"summary"`
}

type rTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      *bool           `json:"strict"`
}

// --- response ---

// rEvent is the union of the SSE events we read. Fields not used by an event
// type stay zero.
type rEvent struct {
	Type      string `json:"type"`
	Delta     string `json:"delta"`
	Arguments string `json:"arguments"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Item      rItem  `json:"item"`
	Response  struct {
		Status string `json:"status"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Usage *rUsage `json:"usage"`
	} `json:"response"`
}

type rItem struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type rUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails *struct {
		CachedTokens     int `json:"cached_tokens"`
		CacheWriteTokens int `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
}

type codexDecoder struct {
	b        *builder
	reason   StopReason
	finished bool // saw a terminal response event
}

func (d *codexDecoder) Event(data string) error {
	var ev rEvent
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return fmt.Errorf("decode stream event: %w", err)
	}
	switch ev.Type {
	case "response.output_item.added":
		d.itemAdded(ev.Item)
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		d.b.Thinking(ev.Delta)
	case "response.reasoning_summary_part.done":
		d.b.Thinking("\n\n")
	case "response.output_text.delta", "response.refusal.delta":
		d.b.Text(ev.Delta)
	case "response.function_call_arguments.delta":
		d.b.ToolCallArgs(ev.Delta)
	case "response.output_item.done":
		d.itemDone(ev.Item)
	case "response.completed", "response.done", "response.incomplete":
		return d.completed(ev)
	case "response.failed":
		return codexFailure(ev)
	case "error":
		return fmt.Errorf("codex: %s", firstNonEmpty(ev.Message, ev.Code, data))
	}
	return nil
}

func (d *codexDecoder) End() (StopReason, error) {
	if !d.finished {
		return "", errors.New("stream ended before the response was complete")
	}
	return d.reason, nil
}

// itemAdded opens the block for a new output item. Text blocks open lazily on
// their first delta.
func (d *codexDecoder) itemAdded(item rItem) {
	switch item.Type {
	case "reasoning":
		d.b.Thinking("")
	case "function_call":
		d.b.BeginToolCall(item.CallID, item.Name)
		d.b.ToolCallArgs(item.Arguments)
	}
}

// itemDone closes the block. The finished item carries the full arguments, so
// a tool call that streamed none picks them up here.
func (d *codexDecoder) itemDone(item rItem) {
	if item.Type == "function_call" && !d.b.HasToolArgs() {
		d.b.ToolCallArgs(item.Arguments)
	}
	d.b.Close()
}

func (d *codexDecoder) completed(ev rEvent) error {
	d.finished = true
	if u := ev.Response.Usage; u != nil {
		cacheRead, cacheWrite := 0, 0
		if u.InputTokensDetails != nil {
			cacheRead = u.InputTokensDetails.CachedTokens
			cacheWrite = u.InputTokensDetails.CacheWriteTokens
		}
		d.b.msg.Usage = Usage{
			Input:       max(0, u.InputTokens-cacheRead-cacheWrite),
			Output:      u.OutputTokens,
			CacheRead:   cacheRead,
			CacheWrite:  cacheWrite,
			TotalTokens: u.TotalTokens,
		}
	}
	if ev.Response.Status != "incomplete" && ev.Type != "response.incomplete" {
		return nil
	}
	details := ev.Response.IncompleteDetails
	if details != nil && details.Reason == "max_output_tokens" {
		d.reason = StopLength
		return nil
	}
	if details != nil && details.Reason != "" {
		return errors.New("response incomplete: " + details.Reason)
	}
	return errors.New("response incomplete")
}

func codexFailure(ev rEvent) error {
	if e := ev.Response.Error; e != nil {
		return fmt.Errorf("codex: %s: %s", e.Code, e.Message)
	}
	return errors.New("codex: response failed")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// codexURL builds the endpoint from a base URL, accepting the bare backend
// URL, ".../codex" or the full ".../codex/responses".
func codexURL(base string) string {
	if strings.TrimSpace(base) == "" {
		base = DefaultCodexBaseURL
	}
	base = strings.TrimRight(base, "/")
	switch {
	case strings.HasSuffix(base, "/codex/responses"):
		return base
	case strings.HasSuffix(base, "/codex"):
		return base + "/responses"
	}
	return base + "/codex/responses"
}

func codexBody(m Model, c Context, o Options) rRequest {
	req := rRequest{
		Model: m.ID,
		// The backend rejects store:true.
		Store:             false,
		Stream:            true,
		Instructions:      c.SystemPrompt,
		Input:             codexInput(c.Messages),
		ToolChoice:        "auto",
		ParallelToolCalls: true,
		Temperature:       o.Temperature,
		PromptCacheKey:    o.SessionID,
	}
	if req.Instructions == "" {
		// The backend requires instructions.
		req.Instructions = "You are a helpful assistant."
	}
	if o.Reasoning != "" {
		req.Reasoning = &rReasoning{Effort: o.Reasoning, Summary: "auto"}
	}
	for _, t := range c.Tools {
		req.Tools = append(req.Tools, rTool{Type: "function", Name: t.Name, Description: t.Description, Parameters: t.Parameters})
	}
	return req
}

// codexInput converts the conversation to Responses API input items. As with
// completions, thinking blocks and failed assistant messages are not replayed.
func codexInput(msgs []Message) []any {
	out := []any{}
	for _, msg := range msgs {
		switch msg.Role {
		case RoleUser:
			out = append(out, map[string]any{
				"role":    "user",
				"content": []map[string]any{{"type": "input_text", "text": msg.Text()}},
			})
		case RoleToolResult:
			out = append(out, map[string]any{
				"type":    "function_call_output",
				"call_id": msg.ToolCallID,
				"output":  msg.Text(),
			})
		case RoleAssistant:
			if msg.StopReason == StopError || msg.StopReason == StopAborted {
				continue
			}
			for _, blk := range msg.Content {
				switch blk.Type {
				case BlockText:
					if blk.Text == "" {
						continue
					}
					out = append(out, map[string]any{
						"type":   "message",
						"role":   "assistant",
						"status": "completed",
						"content": []map[string]any{{
							"type": "output_text", "text": blk.Text, "annotations": []any{},
						}},
					})
				case BlockToolCall:
					out = append(out, map[string]any{
						"type":      "function_call",
						"call_id":   blk.ID,
						"name":      blk.Name,
						"arguments": string(blk.Arguments),
					})
				}
			}
		}
	}
	return out
}

// codexAccountID reads the ChatGPT account ID from the access token's JWT
// payload. The signature is not checked: the ID is only a request header.
func codexAccountID(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("openai-codex access token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return "", fmt.Errorf("decode access token: %w", err)
	}
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("decode access token: %w", err)
	}
	var auth struct {
		AccountID string `json:"chatgpt_account_id"`
	}
	if err := json.Unmarshal(claims[codexJWTClaim], &auth); err != nil || auth.AccountID == "" {
		return "", errors.New("access token has no ChatGPT account ID")
	}
	return auth.AccountID, nil
}
