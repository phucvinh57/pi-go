package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/phucvinh57/pi-go/internal/prompt"
)

// askMsg opens a question on screen; the answer goes back through req.reply.
type askMsg struct{ req *askReq }

// askCancelMsg closes req's question when the asker gave up on it.
type askCancelMsg struct{ req *askReq }

type askReq struct {
	title   string
	options []string // choose from these; empty means type a line
	secret  bool

	reply chan askReply // buffered, so answering never blocks the UI
}

type askReply struct {
	idx  int
	text string
	err  error
}

// asker is the prompt.Prompter a slash command gets: its questions appear in
// the session instead of on a terminal the full-screen view already owns.
type asker struct{ send func(tea.Msg) }

func (a asker) Select(ctx context.Context, title string, options []string) (int, error) {
	r, err := a.ask(ctx, &askReq{title: title, options: options})
	return r.idx, err
}

func (a asker) Input(ctx context.Context, title string, secret bool) (string, error) {
	r, err := a.ask(ctx, &askReq{title: title, secret: secret})
	return r.text, err
}

func (a asker) ask(ctx context.Context, req *askReq) (askReply, error) {
	req.reply = make(chan askReply, 1)
	a.send(askMsg{req})
	select {
	case r := <-req.reply:
		return r, r.err
	case <-ctx.Done():
		a.send(askCancelMsg{req})
		return askReply{}, ctx.Err()
	}
}

// question is the open question: a list or a text field.
type question struct {
	req    *askReq
	picker *picker
	field  prompt.Field
}

func newQuestion(req *askReq) *question {
	q := &question{req: req, field: prompt.Field{Secret: req.secret}}
	if len(req.options) > 0 {
		q.picker = newPicker(req.title, req.options, "")
	}
	return q
}

func (q *question) view() string {
	if q.picker != nil {
		return q.picker.view()
	}
	return promptStyle.Render(q.req.title) + " " + q.field.View() +
		dimStyle.Render("  enter submit · esc cancel")
}

// answer closes the question with reply.
func (m *app) answer(reply askReply) tea.Cmd {
	m.ask.req.reply <- reply
	m.ask = nil
	return nil
}

// onAskKey feeds a key or paste to the open question.
func (m *app) onAskKey(msg tea.Msg) tea.Cmd {
	q := m.ask
	if q.picker != nil {
		k, ok := msg.(tea.KeyPressMsg)
		if !ok {
			return nil
		}
		switch k.String() {
		case "up", "k":
			q.picker.move(-1)
		case "down", "j":
			q.picker.move(1)
		case "enter":
			return m.answer(askReply{idx: q.picker.selected})
		case "esc", "ctrl+c":
			return m.answer(askReply{err: prompt.ErrCancelled})
		}
		return nil
	}
	switch q.field.Update(msg) {
	case prompt.Submitted:
		return m.answer(askReply{text: q.field.Value()})
	case prompt.Dismissed:
		return m.answer(askReply{err: prompt.ErrCancelled})
	}
	return nil
}
