package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Provider streams one assistant response.
//
// Stream never fails synchronously. Every problem, including a bad request or
// an unreachable server, arrives as a terminal EventError. The channel carries
// exactly one terminal event (EventDone or EventError) and is then closed. The
// caller must read until it is closed: the producer goroutine blocks on the
// terminal event. Cancelling ctx stops the request promptly and ends the
// stream with an error event whose message has StopAborted.
type Provider interface {
	Stream(ctx context.Context, model Model, c Context, o Options) <-chan Event
}

// EventType names a streaming event.
type EventType string

const (
	EventStart         EventType = "start"
	EventTextStart     EventType = "text_start"
	EventTextDelta     EventType = "text_delta"
	EventTextEnd       EventType = "text_end"
	EventThinkingStart EventType = "thinking_start"
	EventThinkingDelta EventType = "thinking_delta"
	EventThinkingEnd   EventType = "thinking_end"
	EventToolCallStart EventType = "toolcall_start"
	EventToolCallDelta EventType = "toolcall_delta"
	EventToolCallEnd   EventType = "toolcall_end"
	EventDone          EventType = "done"
	EventError         EventType = "error"
)

// Event is one step of a streaming response. Blocks are numbered by Index in
// the order they appear in the final message; each is opened with a *_start
// event, grown by *_delta events and closed by an *_end event.
type Event struct {
	Type  EventType
	Index int
	// Delta is the new text (or, for tool calls, argument JSON) of a delta
	// event. On *_end events it is the block's complete text; for tool calls
	// see ToolCall.
	Delta string
	// ToolCall is the finished call, on EventToolCallEnd.
	ToolCall *Block
	// Message is the assistant message so far on EventStart (empty content),
	// and the finished message on EventDone and EventError.
	Message *Message
}

// Terminal reports whether the event ends the stream.
func (e Event) Terminal() bool { return e.Type == EventDone || e.Type == EventError }

// builder assembles the assistant message and emits the matching events. The
// protocol adapters feed it raw pieces (text, thinking, tool call fragments)
// and it handles block boundaries, so adapters stay short and every provider
// produces the same event sequence. At most one block is open at a time, which
// matches how both supported APIs stream.
type builder struct {
	ctx context.Context
	out chan<- Event
	msg Message

	open    BlockType // type of the open block, "" when none
	partial []byte    // argument JSON of the open tool call
}

func newBuilder(ctx context.Context, out chan<- Event, m Model) *builder {
	b := &builder{
		ctx: ctx,
		out: out,
		msg: Message{Role: RoleAssistant, Provider: m.Provider, Model: m.ID, Timestamp: time.Now()},
	}
	start := b.msg
	b.send(Event{Type: EventStart, Message: &start})
	return b
}

// send delivers a non-terminal event, giving up if the caller cancelled.
func (b *builder) send(e Event) {
	select {
	case b.out <- e:
	case <-b.ctx.Done():
	}
}

func (b *builder) index() int { return len(b.msg.Content) - 1 }

// Text appends to the open text block, opening one if needed.
func (b *builder) Text(delta string) { b.append(BlockText, EventTextStart, EventTextDelta, delta) }

// Thinking appends to the open thinking block, opening one if needed.
func (b *builder) Thinking(delta string) {
	b.append(BlockThinking, EventThinkingStart, EventThinkingDelta, delta)
}

func (b *builder) append(t BlockType, start, delta EventType, s string) {
	if b.open != t {
		b.Close()
		b.msg.Content = append(b.msg.Content, Block{Type: t})
		b.open = t
		b.send(Event{Type: start, Index: b.index()})
	}
	if s == "" {
		return
	}
	b.msg.Content[b.index()].Text += s
	b.send(Event{Type: delta, Index: b.index(), Delta: s})
}

// BeginToolCall closes the open block and starts a tool call.
func (b *builder) BeginToolCall(id, name string) {
	b.Close()
	b.msg.Content = append(b.msg.Content, Block{Type: BlockToolCall, ID: id, Name: name})
	b.open = BlockToolCall
	b.partial = b.partial[:0]
	b.send(Event{Type: EventToolCallStart, Index: b.index()})
}

// ToolCallArgs appends a fragment of the open tool call's argument JSON.
func (b *builder) ToolCallArgs(delta string) {
	if b.open != BlockToolCall || delta == "" {
		return
	}
	b.partial = append(b.partial, delta...)
	b.send(Event{Type: EventToolCallDelta, Index: b.index(), Delta: delta})
}

// HasToolArgs reports whether the open tool call has received arguments.
func (b *builder) HasToolArgs() bool { return len(b.partial) > 0 }

// Close ends the open block, if any.
func (b *builder) Close() {
	if b.open == "" {
		return
	}
	i := b.index()
	blk := &b.msg.Content[i]
	switch b.open {
	case BlockText:
		b.send(Event{Type: EventTextEnd, Index: i, Delta: blk.Text})
	case BlockThinking:
		b.send(Event{Type: EventThinkingEnd, Index: i, Delta: blk.Text})
	case BlockToolCall:
		blk.Arguments = normalizeArgs(b.partial)
		call := *blk
		b.send(Event{Type: EventToolCallEnd, Index: i, ToolCall: &call})
	}
	b.open = ""
}

// normalizeArgs makes tool arguments a JSON object. Models sometimes send
// nothing for a tool with no parameters; invalid JSON is kept as a string so
// the failure surfaces when the tool is run instead of being lost here.
func normalizeArgs(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	if json.Valid(raw) {
		return json.RawMessage(append([]byte(nil), raw...))
	}
	quoted, _ := json.Marshal(string(raw))
	return quoted
}

// Finish ends the stream with a terminal event. reason is used when err is
// nil; a non-nil err turns into StopAborted if the context was cancelled and
// StopError otherwise.
func (b *builder) Finish(reason StopReason, err error) {
	b.Close()
	if err == nil && len(b.msg.ToolCalls()) > 0 && reason == StopStop {
		// Some servers report "stop" even when the turn ends in tool calls.
		reason = StopToolUse
	}
	b.msg.StopReason = reason
	typ := EventDone
	if err != nil {
		typ = EventError
		b.msg.StopReason = StopError
		b.msg.ErrorMessage = err.Error()
		if b.ctx.Err() != nil {
			b.msg.StopReason = StopAborted
			b.msg.ErrorMessage = "request aborted"
		}
	}
	final := b.msg
	// Terminal events are always delivered, even after cancellation: the
	// caller is told to drain the channel.
	b.out <- Event{Type: typ, Message: &final}
}

// httpError is a non-2xx response from a provider.
type httpError struct {
	Status int
	Body   string
}

func (e *httpError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("provider returned HTTP %d", e.Status)
	}
	return fmt.Sprintf("provider returned HTTP %d: %s", e.Status, e.Body)
}
