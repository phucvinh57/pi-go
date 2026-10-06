// Package agent runs the agent loop: send the conversation to a model, run
// the tools it asks for, send the results back, and repeat until it answers
// without asking for a tool.
//
// An Agent owns one conversation. Prompt adds a user message and runs the loop,
// so a second call continues where the first stopped. The package is the one
// place that joins ai (models) and tools; both are used through their
// interfaces, so tests drive the loop with a scripted ai.Provider and real
// tools on a temp dir.
package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/phucvinh57/pi-go/internal/ai"
	"github.com/phucvinh57/pi-go/internal/tools"
)

// DefaultMaxTurns bounds how many model calls one prompt may take, so a model
// stuck calling tools in circles cannot run forever.
const DefaultMaxTurns = 25

// Config is everything an Agent needs.
type Config struct {
	Provider ai.Provider
	Model    ai.Model
	Options  ai.Options
	// SystemPrompt is sent with every request.
	SystemPrompt string
	Tools        []tools.Tool
	// MaxTurns is the limit of model calls per Prompt; 0 means DefaultMaxTurns.
	MaxTurns int
	// Subscription says the model is paid by a subscription, so the cost in
	// Stats is what the tokens would cost, not what is billed.
	Subscription bool
	// Recorder, if set, is handed every message as it is created.
	Recorder Recorder
}

// Recorder keeps the conversation, for instance in a session file. It is told
// about the user's message, each assistant message and each tool result as
// they happen, in order. That includes an assistant message that ended in an
// error or was aborted, and the messages of a prompt that is then rolled back:
// they were billed, and a record of the session should show them. Record must
// not block for long and cannot fail the prompt, so it has no error to return.
type Recorder interface {
	Record(ai.Message)
}

// Agent is one conversation with a model.
type Agent struct {
	cfg      Config
	specs    []ai.Tool
	known    map[string]bool // tool names, for recovering text-form calls
	callSeq  int             // numbers the calls recovered from text
	messages []ai.Message
	tally    tally // usage of every model call, which rollback does not undo
}

// New creates an Agent with an empty conversation.
func New(cfg Config) *Agent {
	if cfg.MaxTurns <= 0 {
		cfg.MaxTurns = DefaultMaxTurns
	}
	specs := make([]ai.Tool, len(cfg.Tools))
	known := make(map[string]bool, len(cfg.Tools))
	for i, t := range cfg.Tools {
		s := t.Spec()
		known[s.Name] = true
		specs[i] = ai.Tool{Name: s.Name, Description: s.Description, Parameters: s.Parameters}
	}
	return &Agent{cfg: cfg, specs: specs, known: known}
}

// SetModel switches the model used from the next Prompt on. The conversation
// and the usage counted so far are kept. It must not be called while a Prompt
// is running.
func (a *Agent) SetModel(p ai.Provider, m ai.Model, o ai.Options, subscription bool) {
	a.cfg.Provider, a.cfg.Model, a.cfg.Options, a.cfg.Subscription = p, m, o, subscription
}

// SetReasoning changes the reasoning effort used from the next Prompt on; empty
// means the model's default. Like SetModel, it must not be called while a Prompt
// is running.
func (a *Agent) SetReasoning(level string) { a.cfg.Options.Reasoning = level }

func (a *Agent) Messages() []ai.Message { return append([]ai.Message(nil), a.messages...) }

func (a *Agent) Prompt(ctx context.Context, text string) (ai.Message, error) {
	return a.PromptWith(ctx, text, nil)
}

func (a *Agent) PromptWith(ctx context.Context, text string, emit func(Event)) (ai.Message, error) {
	if emit == nil {
		emit = func(Event) {}
	}
	mark := len(a.messages)
	reply, err := a.run(ctx, text, emit)
	if err != nil {
		a.messages = a.messages[:mark]
		// The conversation got shorter, so its size changed; the totals did not.
		a.emitStats(emit)
		return ai.Message{}, err
	}
	return reply, nil
}

// add appends a message to the conversation and records it.
func (a *Agent) add(m ai.Message) {
	a.messages = append(a.messages, m)
	a.record(m)
}

func (a *Agent) record(m ai.Message) {
	if a.cfg.Recorder != nil {
		a.cfg.Recorder.Record(m)
	}
}

func (a *Agent) emitStats(emit func(Event)) {
	s := a.Stats()
	emit(Event{Type: EventStats, Stats: &s})
}

func (a *Agent) run(ctx context.Context, text string, emit func(Event)) (ai.Message, error) {
	a.add(ai.UserText(text))

	for turn := 0; turn < a.cfg.MaxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			return ai.Message{}, err
		}
		reply, err := a.complete(ctx, emit)
		if err != nil {
			return ai.Message{}, fmt.Errorf("%s: %w", a.cfg.Model.Ref(), err)
		}
		reply = recoverToolCalls(reply, a.known, &a.callSeq)
		a.add(reply)
		a.emitStats(emit)

		calls := reply.ToolCalls()
		if len(calls) == 0 {
			return reply, nil
		}
		for _, call := range calls {
			a.add(a.runTool(ctx, call, emit))
		}
	}
	return ai.Message{}, fmt.Errorf("stopped after %d model calls without a final answer", a.cfg.MaxTurns)
}

// complete makes one model call and returns the finished assistant message,
// forwarding the streamed text to emit. A message that ended in error or was
// aborted comes back together with an error. Its usage is counted either way,
// because the provider may have billed a call that did not finish.
func (a *Agent) complete(ctx context.Context, emit func(Event)) (ai.Message, error) {
	var final ai.Message
	stream := a.cfg.Provider.Stream(ctx, a.cfg.Model, ai.Context{
		SystemPrompt: a.cfg.SystemPrompt,
		Messages:     a.messages,
		Tools:        a.specs,
	}, a.cfg.Options)
	for ev := range stream { // drain to the end: the producer blocks on the terminal event
		switch ev.Type {
		case ai.EventTextDelta:
			emit(Event{Type: EventText, Text: ev.Delta})
		case ai.EventThinkingDelta:
			emit(Event{Type: EventThinking, Text: ev.Delta})
		}
		if ev.Terminal() {
			final = *ev.Message
		}
	}
	a.tally.record(a.cfg.Model.Ref(), final.Usage)
	if final.StopReason == ai.StopError || final.StopReason == ai.StopAborted {
		// Not part of the conversation, but part of the record of what happened.
		a.record(final)
		a.emitStats(emit)
		return final, errors.New(final.ErrorMessage)
	}
	// No stats event yet: the reply is not in the conversation until run adds
	// it, and the context size depends on it.
	emit(Event{Type: EventTurnEnd, Usage: final.Usage})
	return final, nil
}

// runTool executes one tool call. A failure, including a call to a tool that
// does not exist, becomes an error result the model can react to; it never
// ends the loop.
func (a *Agent) runTool(ctx context.Context, call ai.Block, emit func(Event)) ai.Message {
	emit(Event{Type: EventToolStart, Tool: call.Name, Args: call.Arguments})
	res := a.execute(ctx, call)
	emit(Event{Type: EventToolEnd, Tool: call.Name, Text: res.Text(), IsError: res.IsError})
	return res
}

func (a *Agent) execute(ctx context.Context, call ai.Block) ai.Message {
	tool, ok := tools.Find(a.cfg.Tools, call.Name)
	if !ok {
		return ai.ToolResult(call.ID, call.Name, fmt.Sprintf("Unknown tool %q", call.Name), true)
	}
	res, err := tool.Execute(ctx, call.Arguments, nil)
	if err != nil {
		msg := err.Error()
		if errors.Is(err, context.Canceled) {
			msg = "Operation aborted"
		}
		return ai.ToolResult(call.ID, call.Name, msg, true)
	}
	return ai.ToolResult(call.ID, call.Name, res.Text, false)
}
