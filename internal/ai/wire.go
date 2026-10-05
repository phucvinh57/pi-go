package ai

import "context"

// wire is one HTTP streaming protocol. Both supported APIs work the same way:
// POST a JSON body, read server-sent events, feed what arrives to a builder.
// The shared driver (wireProvider) does the plumbing; a wire only says how to
// build the request and how to read the events.
type wire interface {
	// Request builds the HTTP request for one model call.
	Request(m Model, c Context, o Options) (httpRequest, error)
	// NewDecoder returns a fresh decoder for one response. Decoders hold
	// per-response state, so they are never shared between calls.
	NewDecoder(b *builder) decoder
}

// decoder turns the events of one response into builder calls.
type decoder interface {
	// Event handles the data of one server-sent event. An error aborts the
	// response.
	Event(data string) error
	// End is called when the stream closes cleanly. It returns why the
	// response ended, or an error if the stream stopped before it was complete.
	End() (StopReason, error)
}

// httpRequest is what a wire asks the driver to send.
type httpRequest struct {
	URL     string
	Headers map[string]string
	Body    any
}

// wireProvider adapts a wire to the Provider interface.
type wireProvider struct{ wire wire }

var (
	_ Provider = wireProvider{}
	_ wire     = completions{}
	_ wire     = codex{}
	_ decoder  = (*completionsDecoder)(nil)
	_ decoder  = (*codexDecoder)(nil)
)

func (p wireProvider) Stream(ctx context.Context, m Model, c Context, o Options) <-chan Event {
	out := make(chan Event)
	go func() {
		defer close(out)
		b := newBuilder(ctx, out, m)
		reason, err := p.run(ctx, b, m, c, o)
		b.Finish(reason, err)
	}()
	return out
}

func (p wireProvider) run(ctx context.Context, b *builder, m Model, c Context, o Options) (StopReason, error) {
	req, err := p.wire.Request(m, c, o)
	if err != nil {
		return "", err
	}
	resp, err := postStream(ctx, req.URL, req.Headers, req.Body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	dec := p.wire.NewDecoder(b)
	err = readSSE(resp.Body, func(_, data string) error { return dec.Event(data) })
	if err != nil {
		return "", err
	}
	return dec.End()
}
