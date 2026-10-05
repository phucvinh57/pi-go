package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// updateInterval throttles onUpdate while a command streams output.
	updateInterval = 100 * time.Millisecond
	// killGrace is how long Run waits for output pipes to close after the
	// shell is killed, in case a background child still holds them.
	killGrace = 2 * time.Second
)

type bash struct{ cwd string }

// NewBash returns the bash tool.
func NewBash(cwd string) Tool { return bash{cwd} }

// BashDetails is Result.Details of a bash call.
type BashDetails struct {
	ExitCode int `json:"exitCode"`
	// Truncation is set when the output was cut; FullOutputPath then names a
	// temp file holding all of it.
	Truncation     *Truncation `json:"truncation,omitempty"`
	FullOutputPath string      `json:"fullOutputPath,omitempty"`
}

func (bash) Spec() Spec {
	return Spec{
		Name: "bash",
		Description: fmt.Sprintf("Execute a bash command in the current working directory. Returns stdout and stderr. "+
			"Output is truncated to last %d lines or %dKB (whichever is hit first). If truncated, full output "+
			"is saved to a temp file. Optionally provide a timeout in seconds.", MaxLines, MaxBytes/1024),
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "command": {"type": "string", "description": "Shell command to execute"},
    "timeout": {"type": "number", "description": "Timeout in seconds (optional, no default timeout)"}
  },
  "required": ["command"]
}`),
		Snippet: "Execute bash commands (ls, grep, find, etc.)",
	}
}

func (b bash) Execute(ctx context.Context, raw json.RawMessage, onUpdate func(Result)) (Result, error) {
	var a struct {
		Command string   `json:"command"`
		Timeout *float64 `json:"timeout"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return Result{}, err
	}
	if a.Command == "" {
		return Result{}, errors.New("command is required")
	}
	var timeout time.Duration
	if a.Timeout != nil {
		secs := *a.Timeout
		if math.IsNaN(secs) || secs <= 0 || secs > float64(math.MaxInt64/time.Second) {
			return Result{}, errors.New("Invalid timeout: must be a positive number of seconds")
		}
		timeout = time.Duration(secs * float64(time.Second))
	}
	if info, err := os.Stat(b.cwd); err != nil || !info.IsDir() {
		return Result{}, fmt.Errorf("Working directory does not exist: %s\nCannot execute bash commands.", b.cwd)
	}

	runCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(runCtx, shellPath(), "-c", a.Command)
	cmd.Dir = b.cwd
	cmd.WaitDelay = killGrace
	killGroupOnCancel(cmd)
	out := &output{onUpdate: onUpdate}
	cmd.Stdout, cmd.Stderr = out, out // one writer: stdout and stderr interleave in order

	runErr := cmd.Run()
	text, details := out.finish()

	switch {
	case ctx.Err() != nil:
		return Result{}, withStatus(text, "Command aborted")
	case runCtx.Err() != nil:
		return Result{}, withStatus(text, fmt.Sprintf("Command timed out after %s seconds", trimFloat(*a.Timeout)))
	}
	if runErr != nil && !errors.Is(runErr, exec.ErrWaitDelay) {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return Result{}, fmt.Errorf("failed to run shell: %w", runErr)
		}
	}

	details.ExitCode = exitCode(cmd.ProcessState)
	if text == "" {
		text = "(no output)"
	}
	if details.ExitCode != 0 {
		return Result{}, withStatus(text, fmt.Sprintf("Command exited with code %d", details.ExitCode))
	}
	return Result{Text: text, Details: details}, nil
}

func shellPath() string {
	if p, err := exec.LookPath("bash"); err == nil {
		return p
	}
	return "sh"
}

func withStatus(text, status string) error {
	if text == "" {
		return errors.New(status)
	}
	return fmt.Errorf("%s\n\n%s", text, status)
}

func trimFloat(f float64) string { return fmt.Sprintf("%g", f) }

// output collects a command's combined output. It keeps only a bounded tail in
// memory and, once the output outgrows the limits, also writes all of it to a
// temp file so nothing is lost when the model sees only the tail.
type output struct {
	onUpdate func(Result)

	mu            sync.Mutex
	tail          []byte
	total         int // bytes seen
	newlines      int
	lastLineBytes int
	lastByte      byte
	file          *os.File
	lastUpdate    time.Time
}

func (o *output) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	o.mu.Lock()
	o.total += len(p)
	o.newlines += bytes.Count(p, []byte{'\n'})
	if i := bytes.LastIndexByte(p, '\n'); i >= 0 {
		o.lastLineBytes = len(p) - i - 1
	} else {
		o.lastLineBytes += len(p)
	}
	o.lastByte = p[len(p)-1]
	o.tail = append(o.tail, p...)

	switch {
	case o.file != nil:
		o.file.Write(p)
	case o.total > MaxBytes || o.lines() > MaxLines:
		// Everything so far is still in tail: it is trimmed only past 2*MaxBytes.
		if f, err := os.CreateTemp("", "pi-go-bash-*.log"); err == nil {
			f.Write(o.tail)
			o.file = f
		}
	}
	if len(o.tail) > 2*MaxBytes {
		keep := o.tail[len(o.tail)-MaxBytes:]
		for len(keep) > 0 && !utf8.RuneStart(keep[0]) {
			keep = keep[1:]
		}
		o.tail = append(o.tail[:0], keep...)
	}

	var update *Result
	if o.onUpdate != nil && time.Since(o.lastUpdate) >= updateInterval {
		o.lastUpdate = time.Now()
		tr := o.truncation()
		update = &Result{Text: tr.Content}
	}
	o.mu.Unlock()

	if update != nil {
		o.onUpdate(*update)
	}
	return len(p), nil
}

// lines counts lines the way splitLines would on the full output.
func (o *output) lines() int {
	if o.total > 0 && o.lastByte != '\n' {
		return o.newlines + 1
	}
	return o.newlines
}

// truncation cuts the retained tail to the limits, with totals that cover all
// of the output, not only what is retained.
func (o *output) truncation() Truncation {
	tr := truncateTail(string(o.tail), MaxLines, MaxBytes)
	if o.total > len(o.tail) && !tr.Truncated {
		tr.Truncated, tr.By = true, "bytes"
	}
	tr.TotalLines, tr.TotalBytes = o.lines(), o.total
	return tr
}

// finish closes the output and returns the text for the model, with a note on
// where the full output went when it was cut.
func (o *output) finish() (string, BashDetails) {
	o.mu.Lock()
	defer o.mu.Unlock()

	var d BashDetails
	tr := o.truncation()
	text := tr.Content
	if o.file != nil {
		d.FullOutputPath = o.file.Name()
		o.file.Close()
	}
	if !tr.Truncated {
		return text, d
	}
	d.Truncation = &tr

	full := ""
	if d.FullOutputPath != "" {
		full = " Full output: " + d.FullOutputPath
	}
	start, end := tr.TotalLines-tr.OutputLines+1, tr.TotalLines
	switch {
	case tr.LastLinePartial:
		text += fmt.Sprintf("\n\n[Showing last %s of line %d (line is %s).%s]",
			formatSize(tr.OutputBytes), end, formatSize(o.lastLineBytes), full)
	case tr.By == "lines":
		text += fmt.Sprintf("\n\n[Showing lines %d-%d of %d.%s]", start, end, tr.TotalLines, full)
	default:
		text += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit).%s]",
			start, end, tr.TotalLines, formatSize(MaxBytes), full)
	}
	return text, d
}
