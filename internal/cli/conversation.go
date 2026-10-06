package cli

import (
	"context"
	"errors"
	"time"

	"github.com/phucvinh57/pi-go/internal/agent"
	"github.com/phucvinh57/pi-go/internal/ai"
	sessionfile "github.com/phucvinh57/pi-go/internal/session"
	"github.com/phucvinh57/pi-go/internal/tools"
)

// conversation is one agent and the file its messages are saved in. The agent
// is built on the first prompt, not up front, so a missing credential or model
// is reported when the user actually asks something, and `pi-go --help` or an
// `auth` subcommand never touches it. It lives as long as the session: each
// prompt continues the conversation, and switching models keeps it.
//
// It holds no lock; session guards it.
type conversation struct {
	env environment

	// noSession turns off saving the conversation (--no-session).
	noSession bool
	agent     *agent.Agent
	// saved is where the conversation is being written; nil until the first
	// prompt, or when saving is off.
	saved *sessionfile.Writer
	// saveErrShown is set once a failure to save has been reported, so the
	// user hears about it once and not after every prompt.
	saveErrShown bool
}

// started reports whether the agent exists yet.
func (c *conversation) started() bool { return c.agent != nil }

// start builds the agent for rm, in plan mode if plan is set, and begins the
// session file. The agent notes in it the model of each call, so with a router
// rm, which only stands in until the router picks, is noted only if the
// router fails and rm answers.
func (c *conversation) start(rm resolvedModel, plan bool, router agent.Router) error {
	if c.env.cwd == "" {
		return errors.New("working directory: cannot be determined")
	}
	var rec agent.Recorder
	if !c.noSession {
		c.saved = sessionfile.NewWriter(c.env.agentDir, c.env.cwd, nil)
		rec = c.saved
	}
	ts := tools.Core(c.env.cwd)
	a := agent.New(agent.Config{
		Provider:     rm.Provider,
		Model:        rm.Model,
		Options:      rm.Options,
		SystemPrompt: agent.SystemPrompt(ts, c.env.cwd, time.Now()),
		PlanPrompt:   agent.PlanSystemPrompt(ts, c.env.cwd, time.Now()),
		Tools:        ts,
		Subscription: rm.Subscription,
		Recorder:     rec,
		Router:       router,
	})
	a.SetPlanMode(plan)
	c.agent = a
	return nil
}

// prompt runs one prompt through the agent. onEvent, if not nil, sees the
// reply and the tool calls as they happen.
func (c *conversation) prompt(ctx context.Context, text string, onEvent func(agent.Event)) (ai.Message, error) {
	return c.agent.PromptWith(ctx, text, onEvent)
}

// setModel switches the running agent to rm, if there is one. The conversation
// is kept, and the agent notes the change in the session file at its next
// call.
func (c *conversation) setModel(rm resolvedModel) {
	if c.agent != nil {
		c.agent.SetModel(rm.Provider, rm.Model, rm.Options, rm.Subscription)
	}
}

// setRouter gives the running agent a router, or with nil takes it away.
func (c *conversation) setRouter(r agent.Router) {
	if c.agent != nil {
		c.agent.SetRouter(r)
	}
}

func (c *conversation) setReasoning(level string) {
	if c.agent != nil {
		c.agent.SetReasoning(level)
	}
}

func (c *conversation) setPlanMode(on bool) {
	if c.agent != nil {
		c.agent.SetPlanMode(on)
	}
}

// takeSaveError returns why the conversation could not be saved, once; after
// that it returns nil.
func (c *conversation) takeSaveError() error {
	if c.saved == nil || c.saveErrShown {
		return nil
	}
	err := c.saved.Err()
	c.saveErrShown = err != nil
	return err
}
