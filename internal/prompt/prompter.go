package prompt

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
)

// ErrNoTerminal is returned by a Prompter that cannot ask the user to choose.
var ErrNoTerminal = errors.New("cannot ask: not running in a terminal")

// Prompter asks the user questions. Both methods return ErrCancelled when the
// user dismisses the question and ctx.Err() when ctx ends first.
type Prompter interface {
	// Select asks the user to pick one of options and returns its index.
	Select(ctx context.Context, title string, options []string) (int, error)
	// Input asks for one line of text; secret input is not echoed.
	Input(ctx context.Context, title string, secret bool) (string, error)
}

type ctxKey struct{}

// With returns a context that carries p. The interactive session uses it to
// let commands ask questions through its own screen.
func With(ctx context.Context, p Prompter) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// From returns the Prompter carried by ctx, or nil.
func From(ctx context.Context) Prompter {
	p, _ := ctx.Value(ctxKey{}).(Prompter)
	return p
}

// Lines is the Prompter for when nobody can be asked interactively: Input
// reads a line from In (a pipe, say) and Select fails.
type Lines struct {
	Out io.Writer

	mu sync.Mutex
	r  *bufio.Reader
}

// NewLines returns a Lines that reads from in and writes questions to out.
func NewLines(in io.Reader, out io.Writer) *Lines {
	return &Lines{Out: out, r: bufio.NewReader(in)}
}

func (l *Lines) Select(context.Context, string, []string) (int, error) {
	return 0, ErrNoTerminal
}

// Input returns io.EOF when the input ends before anything is read.
func (l *Lines) Input(ctx context.Context, title string, _ bool) (string, error) {
	fmt.Fprint(l.Out, title+" ")
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		line, err := l.r.ReadString('\n')
		if err != nil && line == "" {
			ch <- result{err: io.EOF}
			return
		}
		ch <- result{line: line}
	}()
	select {
	case r := <-ch:
		return r.line, r.err
	case <-ctx.Done():
		// The read stays blocked in its goroutine; nothing else reads l.r.
		return "", ctx.Err()
	}
}
