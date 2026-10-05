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

	"pi-go/internal/ai"
	"pi-go/internal/tools"
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
}

// Agent is one conversation with a model.
type Agent struct {
	cfg      Config
	specs    []ai.Tool
	known    map[string]bool // tool names, for recovering text-form calls
	callSeq  int             // numbers the calls recovered from text
	messages []ai.Message
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
// is kept. It must not be called while a Prompt is running.
func (a *Agent) SetModel(p ai.Provider, m ai.Model, o ai.Options) {
	a.cfg.Provider, a.cfg.Model, a.cfg.Options = p, m, o
}

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
		return ai.Message{}, err
	}
	return reply, nil
}

func (a *Agent) run(ctx context.Context, text string, emit func(Event)) (ai.Message, error) {
	a.messages = append(a.messages, ai.UserText(text))

	for turn := 0; turn < a.cfg.MaxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			return ai.Message{}, err
		}
		reply, err := a.complete(ctx, emit)
		if err != nil {
			return ai.Message{}, fmt.Errorf("%s: %w", a.cfg.Model.Ref(), err)
		}
		reply = recoverToolCalls(reply, a.known, &a.callSeq)
		a.messages = append(a.messages, reply)

		calls := reply.ToolCalls()
		if len(calls) == 0 {
			return reply, nil
		}
		for _, call := range calls {
			a.messages = append(a.messages, a.runTool(ctx, call, emit))
		}
	}
	return ai.Message{}, fmt.Errorf("stopped after %d model calls without a final answer", a.cfg.MaxTurns)
}

// complete makes one model call and returns the finished assistant message,
// forwarding the streamed text to emit. A message that ended in error or was
// aborted comes back together with an error.
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
	if final.StopReason == ai.StopError || final.StopReason == ai.StopAborted {
		return final, errors.New(final.ErrorMessage)
	}
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
