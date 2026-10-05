package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
)

type read struct{ cwd string }

// NewRead returns the read tool.
func NewRead(cwd string) Tool { return read{cwd} }

func (read) Spec() Spec {
	return Spec{
		Name: "read",
		Description: fmt.Sprintf("Read the contents of a text file. Output is truncated to %d lines or %dKB "+
			"(whichever is hit first). Use offset/limit for large files. When you need the full file, "+
			"continue with offset until complete.", MaxLines, MaxBytes/1024),
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Path to the file to read (relative or absolute)"},
    "offset": {"type": "number", "description": "Line number to start reading from (1-indexed)"},
    "limit": {"type": "number", "description": "Maximum number of lines to read"}
  },
  "required": ["path"]
}`),
		Snippet:    "Read file contents",
		Guidelines: []string{"Use read to examine files instead of cat or sed."},
	}
}

func (r read) Execute(ctx context.Context, raw json.RawMessage, _ func(Result)) (Result, error) {
	var a struct {
		Path   string   `json:"path"`
		Offset *float64 `json:"offset"`
		Limit  *float64 `json:"limit"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return Result{}, err
	}
	if a.Path == "" {
		return Result{}, errors.New("path is required")
	}
	if a.Limit != nil && *a.Limit < 1 {
		return Result{}, errors.New("limit must be at least 1")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	data, err := os.ReadFile(resolvePath(a.Path, r.cwd))
	if err != nil {
		return Result{}, err
	}
	if mime := http.DetectContentType(data[:min(len(data), 512)]); strings.HasPrefix(mime, "image/") {
		return Result{}, fmt.Errorf("%s is an image (%s); reading images is not supported", a.Path, mime)
	}

	all := strings.Split(string(data), "\n")
	start := 0
	if a.Offset != nil && *a.Offset > 1 {
		start = int(*a.Offset) - 1
	}
	if start >= len(all) {
		return Result{}, fmt.Errorf("Offset %d is beyond end of file (%d lines total)", int(*a.Offset), len(all))
	}

	selected := all[start:]
	userLimited := 0 // lines taken because of limit, when it cut the file short
	if a.Limit != nil && start+int(*a.Limit) < len(all) {
		userLimited = int(*a.Limit)
		selected = all[start : start+userLimited]
	}

	tr := truncateHead(strings.Join(selected, "\n"), MaxLines, MaxBytes)
	first := start + 1
	switch {
	case tr.FirstLineExceedsLimit:
		size := formatSize(len(all[start]))
		return Result{
			Text: fmt.Sprintf("[Line %d is %s, exceeds %s limit. Use bash: sed -n '%dp' %s | head -c %d]",
				first, size, formatSize(MaxBytes), first, a.Path, MaxBytes),
			Details: &tr,
		}, nil
	case tr.Truncated:
		last := first + tr.OutputLines - 1
		limit := ""
		if tr.By == "bytes" {
			limit = fmt.Sprintf(" (%s limit)", formatSize(MaxBytes))
		}
		return Result{
			Text: fmt.Sprintf("%s\n\n[Showing lines %d-%d of %d%s. Use offset=%d to continue.]",
				tr.Content, first, last, len(all), limit, last+1),
			Details: &tr,
		}, nil
	case userLimited > 0:
		remaining := len(all) - (start + userLimited)
		return Result{Text: fmt.Sprintf("%s\n\n[%d more lines in file. Use offset=%d to continue.]",
			tr.Content, remaining, start+userLimited+1)}, nil
	}
	return Result{Text: tr.Content}, nil
}
