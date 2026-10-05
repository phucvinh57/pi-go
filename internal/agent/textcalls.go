package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"pi-go/internal/ai"
)

// Small local models often do not use the structured tool call channel. They
// print the call as text instead: a bare {"name": "bash", "arguments": {...}},
// a fenced JSON block, or Qwen-style <tool_call>...</tool_call> tags. Taken
// literally the loop would treat that as the final answer and run nothing.
// recoverToolCalls turns such text back into tool call blocks.

// wrappers are the markers models put around a text-form call.
var wrappers = regexp.MustCompile("(?i)</?tool_call>|```(?:json)?")

// textCall is the shape of a text-form call. Models use "arguments" or
// "parameters" for the argument object.
type textCall struct {
	Name       string          `json:"name"`
	Arguments  json.RawMessage `json:"arguments"`
	Parameters json.RawMessage `json:"parameters"`
}

// recoverToolCalls looks for calls to known tools in the text of an assistant
// message that has no structured calls. A call counts only when nothing but
// markers and whitespace follows the last one: a call buried in the middle of
// an explanation is more likely a quote than an instruction. The calls are
// removed from the text and appended as tool call blocks, numbered from
// *seq. A message with nothing to recover is returned unchanged.
func recoverToolCalls(msg ai.Message, known map[string]bool, seq *int) ai.Message {
	if len(msg.ToolCalls()) > 0 || msg.StopReason != ai.StopStop {
		return msg
	}

	var content []ai.Block
	var calls []ai.Block
	for _, blk := range msg.Content {
		if blk.Type != ai.BlockText {
			content = append(content, blk)
			continue
		}
		text, found := extractCalls(blk.Text, known, seq)
		if len(found) == 0 {
			content = append(content, blk)
			continue
		}
		calls = append(calls, found...)
		if text != "" {
			content = append(content, ai.Block{Type: ai.BlockText, Text: text})
		}
	}
	if len(calls) == 0 {
		return msg
	}
	msg.Content = append(content, calls...)
	msg.StopReason = ai.StopToolUse
	return msg
}

// extractCalls returns the text without its trailing calls, and the calls.
func extractCalls(text string, known map[string]bool, seq *int) (string, []ai.Block) {
	type found struct {
		start, end int
		name       string
		args       json.RawMessage
	}
	var spans []found
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(text[i:]))
		var raw json.RawMessage
		if dec.Decode(&raw) != nil {
			continue
		}
		end := i + int(dec.InputOffset())
		if name, args, ok := parseTextCall(raw, known); ok {
			spans = append(spans, found{i, end, name, args})
			i = end - 1
		}
	}
	if len(spans) == 0 {
		return text, nil
	}
	if strings.TrimSpace(wrappers.ReplaceAllString(text[spans[len(spans)-1].end:], "")) != "" {
		return text, nil
	}

	var kept strings.Builder
	var calls []ai.Block
	pos := 0
	for _, s := range spans {
		kept.WriteString(text[pos:s.start])
		pos = s.end
		*seq++
		calls = append(calls, ai.Block{Type: ai.BlockToolCall, ID: fmt.Sprintf("text_call_%d", *seq), Name: s.name, Arguments: s.args})
	}
	return strings.TrimSpace(wrappers.ReplaceAllString(kept.String(), "")), calls
}

func parseTextCall(raw json.RawMessage, known map[string]bool) (string, json.RawMessage, bool) {
	var c textCall
	if json.Unmarshal(raw, &c) != nil || !known[c.Name] {
		return "", nil, false
	}
	args := c.Arguments
	if len(args) == 0 {
		args = c.Parameters
	}
	// Some models encode the arguments object as a JSON string.
	var s string
	if json.Unmarshal(args, &s) == nil {
		args = json.RawMessage(s)
	}
	var obj map[string]json.RawMessage
	if len(args) == 0 || json.Unmarshal(args, &obj) != nil {
		return "", nil, false
	}
	return c.Name, args, true
}
